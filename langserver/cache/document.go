// Copyright 2019 Tobias Guggenmos
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cache

import (
	"bytes"
	"context"
	"errors"
	"go/token"
	"sync"

	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/jsonrpc2"
	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/lsp/protocol"
	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/span"
)

// Document caches content, metadata and compile results of a document
// All exported access methods should be threadsafe
type Document struct {
	posData *token.File

	uri        string
	languageID string
	version    float64
	content    string

	mu sync.RWMutex

	versionCtx      context.Context
	obsoleteVersion context.CancelFunc

	queries []*CompiledQuery
	yamls   []*YamlDoc

	diagnostics []protocol.Diagnostic

	// Wait for this before accessing  compileResults
	compilers sync.WaitGroup
}

// ApplyIncrementalChanges applies giver changes to a given Document Content
func (d *Document) ApplyIncrementalChanges(changes []protocol.TextDocumentContentChangeEvent, version float64) (string, error) { //nolint:lll
	d.mu.RLock()

	if version <= d.version {
		return "", jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Update to file didn't increase version number")
	}

	content := []byte(d.content)
	uri := d.uri

	d.mu.RUnlock()

	for _, change := range changes {
		// Update column mapper along with the content.
		converter := span.NewContentConverter(uri, content)
		m := &protocol.ColumnMapper{
			URI:       span.URI(d.uri),
			Converter: converter,
			Content:   content,
		}

		spn, err := m.RangeSpan(*change.Range)

		if err != nil {
			return "", err
		}

		if !spn.HasOffset() {
			return "", jsonrpc2.NewErrorf(jsonrpc2.CodeInternalError, "invalid range for content change")
		}

		start, end := spn.Start().Offset(), spn.End().Offset()
		if end < start {
			return "", jsonrpc2.NewErrorf(jsonrpc2.CodeInternalError, "invalid range for content change")
		}

		var buf bytes.Buffer

		buf.Write(content[:start])
		buf.WriteString(change.Text)
		buf.Write(content[end:])

		content = buf.Bytes()
	}

	return string(content), nil
}

// SetContent sets the content of a document
func (d *Document) SetContent(content string, version float64, new bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !new && version <= d.version {
		return jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Update to file didn't increase version number")
	}

	if len(content) > maxDocumentSize {
		return jsonrpc2.NewErrorf(jsonrpc2.CodeInternalError, "cache/SetContent: Provided.document to large.")
	}

	if !new {
		d.obsoleteVersion()
	}

	d.versionCtx, d.obsoleteVersion = context.WithCancel(context.Background())

	d.content = content
	d.version = version

	// An additional newline is appended, to make sure the last line is indexed
	d.posData.SetLinesForContent(append([]byte(content), '\n'))

	d.queries = []*CompiledQuery{}
	d.yamls = []*YamlDoc{}
	d.diagnostics = []protocol.Diagnostic{}

	d.compilers.Add(1)

	go d.compile(d.versionCtx)

	return nil
}

// GetContent returns the content of a document
// It expects a context.Context retrieved using cache.GetDocument
// and returns an error if that context has expired, i.e. the Document
// has changed since
func (d *Document) GetContent(ctx context.Context) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
		return d.content, nil
	}
}

// GetSubstring returns a substring of the content of a document
// It expects a context.Context retrieved using cache.GetDocument
// and returns an error if that context has expired, i.e. the Document
// has changed since
// The remaining parameters are the start and end of the substring, encoded
// as token.Pos
func (d *Document) GetSubstring(ctx context.Context, pos token.Pos, endPos token.Pos) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
		content := d.content
		base := d.posData.Base()
		pos -= token.Pos(base)
		endPos -= token.Pos(base)

		if pos < 0 || pos > endPos || int(endPos) > len(content) {
			return "", errors.New("invalid range")
		}

		return content[pos:endPos], nil
	}
}

// GetQueries returns the Compilation Results of a document
// It expects a context.Context retrieved using cache.GetDocument
// and returns an error if that context has expired, i.e. the Document
// has changed since
// It blocks until all compile tasks are finished
func (d *Document) GetQueries(ctx context.Context) ([]*CompiledQuery, error) {
	d.compilers.Wait()

	d.mu.RLock()
	defer d.mu.RUnlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return d.queries, nil
	}
}

// GetQuery returns a successfully compiled query at the given position, if there is one
// Otherwise an error will be returned
// It expects a context.Context retrieved using cache.GetDocument
// and returns an error if that context has expired, i.e. the Document
// has changed since
// It blocks until all compile tasks are finished
func (d *Document) GetQuery(ctx context.Context, pos token.Pos) (*CompiledQuery, error) {
	queries, err := d.GetQueries(ctx)
	if err != nil {
		return nil, err
	}

	for _, query := range queries {
		if query.Ast != nil && query.Ast.Pos() <= pos && query.Ast.EndPos() > pos {
			return query, nil
		}
	}

	return nil, errors.New("no query found at given position")
}

// GetVersion returns the content of a document
// It expects a context.Context retrieved using cache.GetDocument
// and returns an error if that context has expired, i.e. the Document
// has changed since
func (d *Document) GetVersion(ctx context.Context) (float64, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
		return d.version, nil
	}
}

// GetURI returns the content of a document
// Since the URI never changes, it does not block or return errors
func (d *Document) GetURI() string {
	return d.uri
}

// GetLanguageID returns the content of a document
// Since the URI never changes, it does not block or return errors
func (d *Document) GetLanguageID() string {
	return d.languageID
}

// GetYamls returns the yaml documents found in the document
// It expects a context.Context retrieved using cache.GetDocument
// and returns an error if that context has expired, i.e. the Document
// has changed since
// It blocks until all compile tasks are finished
func (d *Document) GetYamls(ctx context.Context) ([]*YamlDoc, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return d.yamls, nil
	}
}

// GetDiagnostics returns the Compilation Results of a document
// It expects a context.Context retrieved using cache.GetDocument
// and returns an error if that context has expired, i.e. the Document
// has changed since
// It blocks until all compile tasks are finished
func (d *Document) GetDiagnostics(ctx context.Context) ([]protocol.Diagnostic, error) {
	d.compilers.Wait()

	d.mu.RLock()
	defer d.mu.RUnlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return d.diagnostics, nil
	}
}
