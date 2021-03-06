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

	"github.com/prometheus-community/promql-langserver/vendored/go-tools/jsonrpc2"
	"github.com/prometheus-community/promql-langserver/vendored/go-tools/lsp/protocol"
	"github.com/prometheus-community/promql-langserver/vendored/go-tools/span"
)

// document caches content, metadata and compile results of a document
// All exported access methods should be threadsafe
type document struct {
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
	compilers waitGroup
}

// DocumentHandle bundles a Document together with a context.Context that expires
// when the document changes
type DocumentHandle struct {
	doc *document
	ctx context.Context
}

func (d *DocumentHandle) GetContext() context.Context {
	return d.ctx
}

// ApplyIncrementalChanges applies giver changes to a given Document Content
// The context in the DocumentHandle is ignored
func (d *DocumentHandle) ApplyIncrementalChanges(changes []protocol.TextDocumentContentChangeEvent, version float64) (string, error) {
	d.doc.mu.RLock()
	defer d.doc.mu.RUnlock()

	if version <= d.doc.version {
		return "", jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Update to file didn't increase version number")
	}

	content := []byte(d.doc.content)
	uri := d.doc.uri

	for _, change := range changes {
		// Update column mapper along with the content.
		converter := span.NewContentConverter(uri, content)
		m := &protocol.ColumnMapper{
			URI:       span.URI(d.doc.uri),
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
func (d *DocumentHandle) SetContent(serverLifetime context.Context, content string, version float64, new bool) error {
	d.doc.mu.Lock()
	defer d.doc.mu.Unlock()

	if !new && version <= d.doc.version {
		return jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidParams, "Update to file didn't increase version number")
	}

	if len(content) > maxDocumentSize {
		return jsonrpc2.NewErrorf(jsonrpc2.CodeInternalError, "cache/SetContent: Provided.document to large.")
	}

	if !new {
		d.doc.obsoleteVersion()
	}

	d.doc.versionCtx, d.doc.obsoleteVersion = context.WithCancel(serverLifetime)

	d.doc.content = content
	d.doc.version = version

	// An additional newline is appended, to make sure the last line is indexed
	d.doc.posData.SetLinesForContent(append([]byte(content), '\n'))

	d.doc.queries = []*CompiledQuery{}
	d.doc.yamls = []*YamlDoc{}
	d.doc.diagnostics = []protocol.Diagnostic{}

	d.doc.compilers.Add(1)

	// We need to create a new document handler here since the old one
	// still carries the deprecated version context
	go (&DocumentHandle{d.doc, d.doc.versionCtx}).compile() //nolint:errcheck

	return nil
}

// GetContent returns the content of a document
// and returns an error if that context has expired, i.e. the Document
// has changed since
func (d *DocumentHandle) GetContent() (string, error) {
	d.doc.mu.RLock()
	defer d.doc.mu.RUnlock()

	select {
	case <-d.ctx.Done():
		return "", d.ctx.Err()
	default:
		return d.doc.content, nil
	}
}

// GetSubstring returns a substring of the content of a document
// and returns an error if that context has expired, i.e. the Document
// has changed since
// The remaining parameters are the start and end of the substring, encoded
// as token.Pos
func (d *DocumentHandle) GetSubstring(pos token.Pos, endPos token.Pos) (string, error) {
	d.doc.mu.RLock()
	defer d.doc.mu.RUnlock()

	select {
	case <-d.ctx.Done():
		return "", d.ctx.Err()
	default:
		content := d.doc.content
		base := d.doc.posData.Base()
		pos -= token.Pos(base)
		endPos -= token.Pos(base)

		if pos < 0 || pos > endPos || int(endPos) > len(content) {
			return "", errors.New("invalid range")
		}

		return content[pos:endPos], nil
	}
}

// GetQueries returns the Compilation Results of a document
// and returns an error if that context has expired, i.e. the Document
// has changed since
// It blocks until all compile tasks are finished
func (d *DocumentHandle) GetQueries() ([]*CompiledQuery, error) {
	d.doc.compilers.Wait()

	d.doc.mu.RLock()

	defer d.doc.mu.RUnlock()

	select {
	case <-d.ctx.Done():
		return nil, d.ctx.Err()
	default:
		return d.doc.queries, nil
	}
}

// GetQuery returns a successfully compiled query at the given position, if there is one
// Otherwise an error will be returned
// and returns an error if that context has expired, i.e. the Document
// has changed since
// It blocks until all compile tasks are finished
func (d *DocumentHandle) GetQuery(pos token.Pos) (*CompiledQuery, error) {
	queries, err := d.GetQueries()
	if err != nil {
		return nil, err
	}

	for _, query := range queries {
		if query.Ast != nil && query.Pos <= pos && query.Pos+token.Pos(query.Ast.PositionRange().End) >= pos {
			return query, nil
		}
	}

	return nil, errors.New("no query found at given position")
}

// GetVersion returns the content of a document
// and returns an error if that context has expired, i.e. the Document
// has changed since
func (d *DocumentHandle) GetVersion() (float64, error) {
	d.doc.mu.RLock()
	defer d.doc.mu.RUnlock()

	select {
	case <-d.ctx.Done():
		return 0, d.ctx.Err()
	default:
		return d.doc.version, nil
	}
}

// GetURI returns the content of a document
// Since the URI never changes, it does not block or return errors
func (d *DocumentHandle) GetURI() string {
	return d.doc.uri
}

// GetLanguageID returns the content of a document
// Since the URI never changes, it does not block or return errors
func (d *DocumentHandle) GetLanguageID() string {
	return d.doc.languageID
}

// GetYamls returns the yaml documents found in the document
// and returns an error if that context has expired, i.e. the Document
// has changed since
// It blocks until all compile tasks are finished
func (d *DocumentHandle) GetYamls() ([]*YamlDoc, error) {
	d.doc.mu.RLock()
	defer d.doc.mu.RUnlock()

	select {
	case <-d.ctx.Done():
		return nil, d.ctx.Err()
	default:
		return d.doc.yamls, nil
	}
}

// GetDiagnostics returns the Compilation Results of a document
// and returns an error if that context has expired, i.e. the Document
// has changed since
// It blocks until all compile tasks are finished
func (d *DocumentHandle) GetDiagnostics() ([]protocol.Diagnostic, error) {
	d.doc.compilers.Wait()

	d.doc.mu.RLock()
	defer d.doc.mu.RUnlock()

	select {
	case <-d.ctx.Done():
		return nil, d.ctx.Err()
	default:
		return d.doc.diagnostics, nil
	}
}
