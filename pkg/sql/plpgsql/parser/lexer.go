// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package parser

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgcode"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/plpgsqltree"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	unimp "github.com/cockroachdb/cockroach/pkg/util/errorutil/unimplemented"
	"github.com/cockroachdb/errors"
)

type lexer struct {
	in string
	// tokens contains tokens generated by the scanner.
	tokens []plpgsqlSymType

	// The type that should be used when an INT or SERIAL is encountered.
	nakedIntType *types.T

	// lastPos is the position into the tokens slice of the last
	// token returned by Lex().
	lastPos int

	stmt *plpgsqltree.PLpgSQLStmtBlock

	// numPlaceholders is 1 + the highest placeholder index encountered.
	numPlaceholders int
	numAnnotations  tree.AnnotationIdx

	lastError error

	env *plpgsqltree.PLpgSQLEnv
}

func (l *lexer) init(sql string, tokens []plpgsqlSymType, nakedIntType *types.T) {
	l.in = sql
	l.tokens = tokens
	l.lastPos = -1
	l.stmt = nil
	l.numPlaceholders = 0
	l.numAnnotations = 0
	l.lastError = nil

	l.nakedIntType = nakedIntType
}

// cleanup is used to avoid holding on to memory unnecessarily (for the cases
// where we reuse a scanner).
func (l *lexer) cleanup() {
	l.tokens = nil
	l.stmt = nil
	l.lastError = nil
}

// Lex lexes a token from input.
// to push the tokens back (move l.pos back).
func (l *lexer) Lex(lval *plpgsqlSymType) int {
	l.lastPos++
	// The core lexing takes place in the scanner. Here we do a small bit of post
	// processing of the lexical tokens so that the grammar only requires
	// one-token lookahead despite SQL requiring multi-token lookahead in some
	// cases. These special cases are handled below and the returned tokens are
	// adjusted to reflect the lookahead (LA) that occurred.
	if l.lastPos >= len(l.tokens) {
		lval.id = 0
		lval.pos = int32(len(l.in))
		lval.str = "EOF"
		return 0
	}
	*lval = l.tokens[l.lastPos]

	switch lval.id {
	case RETURN:
		nextToken := plpgsqlSymType{}
		if l.lastPos+1 < len(l.tokens) {
			nextToken = l.tokens[l.lastPos+1]
		}
		// TODO this might not be needed
		//secondNextToken := plpgsqlSymType{}
		//if l.lastPos+2 < len(l.tokens) {
		//	secondNextToken = l.tokens[l.lastPos+2]
		//}
		switch lval.id {
		case RETURN:
			switch nextToken.id {
			case NEXT:
				lval.id = RETURN_NEXT
			case QUERY:
				lval.id = RETURN_QUERY
			}
		}
	}

	return int(lval.id)
}

// MakeExecSqlStmt makes a PLpgSQLStmtExecSql from current token position.
// TODO(chengxiong): we need to fill in variables as well.
func (l *lexer) MakeExecSqlStmt(startTokenID int) *plpgsqltree.PLpgSQLStmtExecSql {
	sqlToks := make([]string, 0)
	if startTokenID == 0 || startTokenID == ';' {
		panic("plpgsql_execsql: invalid start token")
	}
	if int(l.lastToken().id) != startTokenID {
		panic("plpgsql_execsql: given start token does not match current pos of lexer")
	}

	var hasInto bool
	var hasStrict bool
	var preTok plpgsqlSymType
	tok := l.lastToken()
	for {
		if !hasInto {
			sqlToks = append(sqlToks, tok.Str())
		}
		preTok = tok
		l.Lex(&tok)
		if tok.id == ';' {
			break
		}
		if tok.id == 0 {
			panic("unexpected end of function definition")
		}
		if hasInto && tok.id == STRICT {
			hasStrict = true
			continue
		}
		if tok.id == INTO {
			if preTok.id == INSERT {
				continue
			}
			if preTok.id == MERGE {
				continue
			}
			if startTokenID == IMPORT {
				continue
			}
			if hasInto {
				panic("plpgsql_execsql: INTO specified more than once")
			}
			hasInto = true
		}
	}
	return &plpgsqltree.PLpgSQLStmtExecSql{
		SqlStmt: strings.Join(sqlToks, " "),
		Into:    hasInto,
		Strict:  hasStrict,
	}
}

// ReadSqlExpressionStr returns the string from the l.lastPos till it sees
// the terminator for the first time. The returned string is made by tokens
// between the starting index (included) to the terminator (not included).
// TODO(plpgsql-team): pass the output to the sql parser
// (i.e. sqlParserImpl.Parse()).
func (l *lexer) ReadSqlExpressionStr(terminator int) string {
	exprTokenStrs := make([]string, 0)
	parenLevel := 0
	for l.lastPos < len(l.tokens) {
		tok := l.Peek()
		if int(tok.id) == terminator {
			break
		} else if tok.id == '(' || tok.id == '[' {
			parenLevel++

		} else if tok.id == ')' || tok.id == ']' {
			parenLevel--
			if parenLevel < 0 {
				panic("wrongly nested parentheses")
			}
		}
		exprTokenStrs = append(exprTokenStrs, tok.Str())
		l.lastPos++
	}
	if parenLevel != 0 {
		panic("parentheses is badly nested")
	}
	if len(exprTokenStrs) == 0 {
		panic("there should be at least one token for sql expression")
	}

	return strings.Join(exprTokenStrs, " ")
}

// Peek peeks
func (l *lexer) Peek() plpgsqlSymType {
	if l.lastPos+1 < len(l.tokens) {
		return l.tokens[l.lastPos+1]
	}
	return plpgsqlSymType{}
}

// PushBack move the lastP
func (l *lexer) PushBack(n int) {
	if l.lastPos-n >= 0 {
		l.lastPos -= n
	}
}

func (l *lexer) lastToken() plpgsqlSymType {
	if l.lastPos < 0 {
		return plpgsqlSymType{}
	}

	if l.lastPos >= len(l.tokens) {
		return plpgsqlSymType{
			id:  0,
			pos: int32(len(l.in)),
			str: "EOF",
		}
	}
	return l.tokens[l.lastPos]
}

// NewAnnotation returns a new annotation index.
func (l *lexer) NewAnnotation() tree.AnnotationIdx {
	l.numAnnotations++
	return l.numAnnotations
}

// SetStmt is called from the parser when the statement is constructed.
func (l *lexer) SetStmt(stmt plpgsqltree.PLpgSQLStatement) {
	l.stmt = stmt.(*plpgsqltree.PLpgSQLStmtBlock)
}

// UpdateNumPlaceholders is called from the parser when a placeholder is constructed.
func (l *lexer) UpdateNumPlaceholders(p *tree.Placeholder) {
	if n := int(p.Idx) + 1; l.numPlaceholders < n {
		l.numPlaceholders = n
	}
}

// PurposelyUnimplemented wraps Error, setting lastUnimplementedError.
func (l *lexer) PurposelyUnimplemented(feature string, reason string) {
	// We purposely do not use unimp here, as it appends hints to suggest that
	// the error may be actively tracked as a bug.
	l.lastError = errors.WithHint(
		errors.WithTelemetry(
			pgerror.Newf(pgcode.Syntax, "unimplemented: this syntax"),
			fmt.Sprintf("sql.purposely_unimplemented.%s", feature),
		),
		reason,
	)
	l.populateErrorDetails()
	l.lastError = &tree.UnsupportedError{
		Err:         l.lastError,
		FeatureName: feature,
	}
}

// UnimplementedWithIssue wraps Error, setting lastUnimplementedError.
func (l *lexer) UnimplementedWithIssue(issue int) {
	l.lastError = unimp.NewWithIssue(issue, "this syntax")
	l.populateErrorDetails()
	l.lastError = &tree.UnsupportedError{
		Err:         l.lastError,
		FeatureName: fmt.Sprintf("https://github.com/cockroachdb/cockroach/issues/%d", issue),
	}
}

// UnimplementedWithIssueDetail wraps Error, setting lastUnimplementedError.
func (l *lexer) UnimplementedWithIssueDetail(issue int, detail string) {
	l.lastError = unimp.NewWithIssueDetail(issue, detail, "this syntax")
	l.populateErrorDetails()
	l.lastError = &tree.UnsupportedError{
		Err:         l.lastError,
		FeatureName: detail,
	}
}

// Unimplemented wraps Error, setting lastUnimplementedError.
func (l *lexer) Unimplemented(feature string) {
	l.lastError = unimp.New(feature, "this syntax")
	l.populateErrorDetails()
	l.lastError = &tree.UnsupportedError{
		Err:         l.lastError,
		FeatureName: feature,
	}
}

// setErr is called from parsing action rules to register an error observed
// while running the action. That error becomes the actual "cause" of the
// syntax error.
func (l *lexer) setErr(err error) {
	err = pgerror.WithCandidateCode(err, pgcode.Syntax)
	l.lastError = err
	l.populateErrorDetails()
}

func (l *lexer) Error(e string) {
	e = strings.TrimPrefix(e, "syntax error: ") // we'll add it again below.
	l.lastError = pgerror.WithCandidateCode(errors.Newf("%s", e), pgcode.Syntax)
	l.populateErrorDetails()
}

func (l *lexer) populateErrorDetails() {
	lastTok := l.lastToken()

	if lastTok.id == ERROR {
		// This is a tokenizer (lexical) error: the scanner
		// will have stored the error message in the string field.
		err := pgerror.WithCandidateCode(errors.Newf("lexical error: %s", lastTok.str), pgcode.Syntax)
		l.lastError = errors.WithSecondaryError(err, l.lastError)
	} else {
		// This is a contextual error. Print the provided error message
		// and the error context.
		if !strings.Contains(l.lastError.Error(), "syntax error") {
			// "syntax error" is already prepended when the yacc-generated
			// parser encounters a parsing error.
			l.lastError = errors.Wrap(l.lastError, "syntax error")
		}
		l.lastError = errors.Wrapf(l.lastError, "at or near \"%s\"", lastTok.str)
	}

	// Find the end of the line containing the last token.
	i := strings.IndexByte(l.in[lastTok.pos:], '\n')
	if i == -1 {
		i = len(l.in)
	} else {
		i += int(lastTok.pos)
	}
	// Find the beginning of the line containing the last token. Note that
	// LastIndexByte returns -1 if '\n' could not be found.
	j := strings.LastIndexByte(l.in[:lastTok.pos], '\n') + 1
	// Output everything up to and including the line containing the last token.
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "source SQL:\n%s\n", l.in[:i])
	// Output a caret indicating where the last token starts.
	fmt.Fprintf(&buf, "%s^", strings.Repeat(" ", int(lastTok.pos)-j))
	l.lastError = errors.WithDetail(l.lastError, buf.String())
}

// specialHelpErrorPrefix is a special prefix that must be present at
// the start of an error message to be considered a valid help
// response payload by the CLI shell.
const specialHelpErrorPrefix = "help token in input"

func (l *lexer) populateHelpMsg(msg string) {
	l.lastError = errors.WithHint(errors.Wrap(l.lastError, specialHelpErrorPrefix), msg)
}
