---
title: PromQL Parser Improvements
type: Proposal
menu: proposals
status: WIP
owner: slrtbtfs
---

## PromQL Parser Improvements

### Motivation

For the planned PromQL language server (see Proposal 2019_promql-language-server) it is desirable to use the same parser as prometheus to ensure consistency and avoid code duplication.

The current Parser is not sufficient for that use case though.

The proposed changes include adding the necessary features to the parser, improving error handling and cleaning up the existing code Base.

### Technical summary

The proposed changes are intended to improve the metadata generated by the parser, to make it easier to analyze them.

All of the proposed concepts are already successfully employed by the go compiler itself.

To avoid breaking to much existing code, all existing interfaces and APIs are kept, as far as possible.

### Proposed Changes

#### Extend the Node interface of the AST

##### Problem

Currently the Nodes of the abstract syntax tree have to implement this interface:

    type Node interface {
        // String representation of the node that returns the given node when parsed
        // as part of a valid query.
        String() string
    }

This is sufficient for just executing queries, but makes it hard for the language server to map positions in the source code to ast nodes and vice versa.

##### Proposed solution

Extend the interface to the following and change the parser accordingly.

    import "go/token"
    
    type Node interface {
        // String representation of the node that returns the given node when parsed
        // as part of a valid query. Kept there for backwards compatibility
        String() string
        // The following two are how the go compiler defines it's Node interface
        Pos() token.Pos // position of first character belonging to the node
        End() token.Pos // position of first character immediately after the node
    }

`token.Pos` is actually just an int, which can be uniquely mapped to a Position in a file.

    // from go/token/position.go
    // Position describes an arbitrary source position
    // including the file, line, and column location.
    // A Position is valid if the line number is > 0.
    //
    type Position struct {
        Filename string // filename, if any
        Offset   int    // offset, starting at 0
        Line     int    // line number, starting at 1
        Column   int    // column number, starting at 1 (byte count)
    }

It also has the nice property, that the difference between two `token.Pos` from the same file is exactly the difference of the respective postions when the file is seen as a string.

This allows the language server to do a quick binary search in order to map a position in a query or file to the corresponding AST Nodes.
Similarly, if errors or warnings are found by analyzing the AST it's possible to figure out where in the source code the error is.

##### Scope of the change

All code that writes to the AST has to be updated to implement the new interface. This concerns the parser, the lexer and the AST implementation itself.

By only extending the old interface, the change won't break code that only reads the AST.

---
Everything below these lines can be ignored so far

---
#### Add position metadata to errors

##### Problem

Currently errors are just strings. In some cases these Strings include the positions of the error, but that does not happen consistently. Especially for the Language Server use case it would be desirable to know the precise location of an error.

##### Proposed solution

TODO

#### Generate incomplete ASTs

##### Problem

While PromQL Queries are being typed, there 

##### Proposed solution

...

---

1. When an syntax error occurs, the parser aborts and a hard coded string is used to report an error to the user. For the language server errors should be represented by a more advanced data structure which can also store the position of the errors and possible Quick Fixes. The error messages are spread over several source files.
2. For the purposes of autocompletion an AST should still be generated, when some closing parentheses are missing.

Since these changes would also benefit the upstream prometheus implementation and consistence between the language server and prometheus itself is desired, it is proposed that the PromQL language server does not implement it's own parser. Instead all necessary changes should go into the upstream parser.