// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"

	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/lsp/protocol"
	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/lsp/source"
	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/lsp/telemetry"
	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/span"
	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/telemetry/log"
)

func (s *Server) documentHighlight(ctx context.Context, params *protocol.DocumentHighlightParams) ([]protocol.DocumentHighlight, error) {
	uri := span.NewURI(params.TextDocument.URI)
	view := s.session.ViewOf(uri)
	rngs, err := source.Highlight(ctx, view, uri, params.Position)
	if err != nil {
		log.Error(ctx, "no highlight", err, telemetry.URI.Of(uri))
	}
	return toProtocolHighlight(rngs), nil
}

func toProtocolHighlight(rngs []protocol.Range) []protocol.DocumentHighlight {
	result := make([]protocol.DocumentHighlight, 0, len(rngs))
	kind := protocol.Text
	for _, rng := range rngs {
		result = append(result, protocol.DocumentHighlight{
			Kind:  &kind,
			Range: rng,
		})
	}
	return result
}
