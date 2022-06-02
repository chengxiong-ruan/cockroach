// Copyright 2016 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package tree

import (
	"context"
	"fmt"

	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/errors"
	"github.com/cockroachdb/redact"
)

// Function names are used in expressions in the FuncExpr node.
// General syntax:
//    [ <context-prefix> . ] <function-name>
//
// The other syntax nodes hold a mutable ResolvableFunctionReference
// attribute.  This is populated during parsing with an
// UnresolvedName, and gets assigned a FunctionDefinition upon the
// first call to its Resolve() method.

// ResolvableFunctionReference implements the editable reference cell
// of a FuncExpr. The FunctionRerence is updated by the Normalize()
// method.
type ResolvableFunctionReference struct {
	FunctionReference
}

// Format implements the NodeFormatter interface.
func (fn *ResolvableFunctionReference) Format(ctx *FmtCtx) {
	ctx.FormatNode(fn.FunctionReference)
}
func (fn *ResolvableFunctionReference) String() string { return AsString(fn) }

// Resolve checks if the function name is already resolved and
// resolves it as necessary.
// TODO (Chengxiong): UDF refactor so that it takes only SemaContext and a context.Context
func (fn *ResolvableFunctionReference) Resolve(
	semaCtx *SemaContext, searchPath SearchPath, argTypes []*types.T,
) (*FunctionDefinition, error) {
	switch t := fn.FunctionReference.(type) {
	case *FunctionDefinition:
		return t, nil
	case *UnresolvedName:
		if semaCtx != nil && semaCtx.FunctionResolver != nil {
			objName, err := t.ToUnresolvedObjectName(0)
			if err != nil {
				return nil, err
			}
			fnName := objName.ToFunctionName()
			fd, err := semaCtx.FunctionResolver.ResolveFunction(context.Background(), &fnName, argTypes)
			if fd != nil {
				fn.FunctionReference = fd
				return fd, nil
			}
			fd, err = t.ResolveFunction(searchPath)
			if err != nil {
				return nil, err
			}
			fn.FunctionReference = fd
			return fd, nil
		} else {
			fd, err := t.ResolveFunction(searchPath)
			if err != nil {
				return nil, err
			}
			fn.FunctionReference = fd
			return fd, nil
		}
	default:
		return nil, errors.AssertionFailedf("unknown function name type: %+v (%T)",
			fn.FunctionReference, fn.FunctionReference,
		)
	}
}

// WrapFunction creates a new ResolvableFunctionReference
// holding a pre-resolved function. Helper for grammar rules.
func WrapFunction(n string) ResolvableFunctionReference {
	fd, ok := FunDefs[n]
	if !ok {
		panic(errors.AssertionFailedf("function %s() not defined", redact.Safe(n)))
	}
	return ResolvableFunctionReference{fd}
}

// FunctionReference is the common interface to UnresolvedName and QualifiedFunctionName.
type FunctionReference interface {
	fmt.Stringer
	NodeFormatter
	functionReference()
}

func (*UnresolvedName) functionReference()     {}
func (*FunctionDefinition) functionReference() {}
