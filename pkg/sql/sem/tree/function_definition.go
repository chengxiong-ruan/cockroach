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
	"sort"

	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgcode"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/catconstants"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/cockroach/pkg/util/iterutil"
	"github.com/cockroachdb/errors"
	"github.com/lib/pq/oid"
)

// FunctionDefinition implements a reference to the (possibly several)
// overloads for a built-in function.
// TODO(Chengxiong): Remove this struct entirely. Instead, use overloads from
// function resolution or use "GetBuiltinProperties" if the need is to only look
// at builtin functions(there are such existing use cases).
type FunctionDefinition struct {
	// Name is the short name of the function.
	Name string

	// Definition is the set of overloads for this function name.
	Definition []*Overload

	// FunctionProperties are the properties common to all overloads.
	FunctionProperties
}

// ResolvedFunctionDefinition is similar to FunctionDefinition but with all the
// overloads prefixed wit schema name.
type ResolvedFunctionDefinition struct {
	Name string

	// ExplicitSchema is only set to true when the function is resolved with
	// explicit schema name in the desired function name. It means that all
	// overloads are prefixed with the same desired schema name.
	ExplicitSchema bool

	Overloads []*PrefixedOverload
}

// Format implements the NodeFormatter interface.
func (fd *ResolvedFunctionDefinition) Format(ctx *FmtCtx) {
	ctx.WriteString(fd.Name)
}
func (fd *ResolvedFunctionDefinition) String() string { return AsString(fd) }

// MergeWith is used specifically to merge two UDF definitions with
// same name but from different schemas. Currently, the FunctionProperties field
// is not set for UDFs. The merging result does not belong to a ExplicitSchema.
func (fd *ResolvedFunctionDefinition) MergeWith(
	another *ResolvedFunctionDefinition,
) (*ResolvedFunctionDefinition, error) {
	if fd == nil {
		return another, nil
	}
	if another == nil {
		return fd, nil
	}

	if fd.Name != another.Name {
		return nil, errors.Newf("cannot merge function definition of %q with %q", fd.Name, another.Name)
	}

	return &ResolvedFunctionDefinition{
		Name:           fd.Name,
		ExplicitSchema: false,
		Overloads:      append(fd.Overloads, another.Overloads...),
	}, nil
}

// FindExactMatchUDFOverloadInSchema finds overloads from schema with argument
// types match exactly with the given types.
func (fd *ResolvedFunctionDefinition) FindExactMatchUDFOverloadInSchema(
	argTypes []*types.T, schema string,
) (*Overload, error) {
	if schema == "" {
		return nil, errors.New("schema cannot be empty string")
	}
	var ret []*Overload
	for _, o := range fd.Overloads {
		if schema != o.Schema {
			continue
		}
		if o.params().Match(argTypes) {
			ret = append(ret, o.Overload)
		}
	}
	// In theory this shouldn't happen since we defend on the CREATE FUNCTION path.
	if len(ret) > 1 {
		return nil, pgerror.New(pgcode.AmbiguousFunction, "more than one function found exactly matched")
	}
	if len(ret) == 0 {
		return nil, nil
	}

	return ret[0], nil
}

// FindExactMatchUDFOverload looks for all overloads on search path matched
// exactly with the given argument types. Return results are sorted in the order
// they appear in the search path.
func (fd *ResolvedFunctionDefinition) FindExactMatchUDFOverload(
	argTypes []*types.T, path SearchPath,
) []*Overload {
	scNameToIdx := make(map[string]int)
	idx := 0
	path.IterateSearchPath(func(schema string) error {
		scNameToIdx[schema] = idx
		idx++
		return nil
	})

	var prefixed []*PrefixedOverload
	var ret []*Overload
	for _, o := range fd.Overloads {
		_, ok := scNameToIdx[o.Schema]
		if !ok {
			continue
		}
		if o.params().Match(argTypes) {
			ret = append(ret, o.Overload)
			prefixed = append(prefixed, o)
		}
	}

	sort.Slice(ret, func(i, j int) bool {
		return scNameToIdx[prefixed[i].Schema] < scNameToIdx[prefixed[j].Schema]
	})
	return ret
}

type PrefixedOverload struct {
	Schema string
	// TODO(Chengxiong): make this into a *Overload
	*Overload
}

func MakePrefixedOverload(schema string, overload *Overload) *PrefixedOverload {
	return &PrefixedOverload{Schema: schema, Overload: overload}
}

func (o *PrefixedOverload) GetOverload() *Overload {
	return o.Overload
}

// FunctionProperties defines the properties of the built-in
// functions that are common across all overloads.
type FunctionProperties struct {
	// UnsupportedWithIssue, if non-zero indicates the built-in is not
	// really supported; the name is a placeholder. Value -1 just says
	// "not supported" without an issue to link; values > 0 provide an
	// issue number to link.
	UnsupportedWithIssue int

	// Undocumented, when set to true, indicates that the built-in function is
	// hidden from documentation. This is currently used to hide experimental
	// functionality as it is being developed.
	Undocumented bool

	// Private, when set to true, indicates the built-in function is not
	// available for use by user queries. This is currently used by some
	// aggregates due to issue #10495. Private functions are implicitly
	// considered undocumented.
	Private bool

	// DistsqlBlocklist is set to true when a function depends on
	// members of the EvalContext that are not marshaled by DistSQL
	// (e.g. planner). Currently used for DistSQL to determine if
	// expressions can be evaluated on a different node without sending
	// over the EvalContext.
	//
	// TODO(andrei): Get rid of the planner from the EvalContext and then we can
	// get rid of this blocklist.
	DistsqlBlocklist bool

	// Class is the kind of built-in function (normal/aggregate/window/etc.)
	Class FunctionClass

	// Category is used to generate documentation strings.
	Category string

	// AvailableOnPublicSchema indicates whether the function can be resolved
	// if it is found on the public schema.
	AvailableOnPublicSchema bool

	// ReturnLabels can be used to override the return column name of a
	// function in a FROM clause.
	// This satisfies a Postgres quirk where some json functions have
	// different return labels when used in SELECT or FROM clause.
	ReturnLabels []string

	// AmbiguousReturnType is true if the builtin's return type can't be
	// determined without extra context. This is used for formatting builtins
	// with the FmtParsable directive.
	AmbiguousReturnType bool

	// HasSequenceArguments is true if the builtin function takes in a sequence
	// name (string) and can be used in a scalar expression.
	// TODO(richardjcai): When implicit casting is supported, these builtins
	// should take RegClass as the arg type for the sequence name instead of
	// string, we will add a dependency on all RegClass types used in a view.
	HasSequenceArguments bool

	// CompositeInsensitive indicates that this function returns equal results
	// when evaluated on equal inputs. This is a non-trivial property for
	// composite types which can be equal but not identical
	// (e.g. decimals 1.0 and 1.00). For example, converting a decimal to string
	// is not CompositeInsensitive.
	//
	// See memo.CanBeCompositeSensitive.
	CompositeInsensitive bool
}

// ShouldDocument returns whether the built-in function should be included in
// external-facing documentation.
func (fp *FunctionProperties) ShouldDocument() bool {
	return !(fp.Undocumented || fp.Private)
}

// FunctionClass specifies the class of the builtin function.
type FunctionClass int

const (
	// NormalClass is a standard builtin function.
	NormalClass FunctionClass = iota
	// AggregateClass is a builtin aggregate function.
	AggregateClass
	// WindowClass is a builtin window function.
	WindowClass
	// GeneratorClass is a builtin generator function.
	GeneratorClass
	// SQLClass is a builtin function that executes a SQL statement as a side
	// effect of the function call.
	//
	// For example, AddGeometryColumn is a SQLClass function that executes an
	// ALTER TABLE ... ADD COLUMN statement to add a geometry column to an
	// existing table. It returns metadata about the column added.
	//
	// All builtin functions of this class should include a definition for
	// Overload.SQLFn, which returns the SQL statement to be executed. They
	// should also include a definition for Overload.Fn, which is executed
	// like a NormalClass function and returns a Datum.
	SQLClass
)

// Avoid vet warning about unused enum value.
var _ = NormalClass

// NewFunctionDefinition allocates a function definition corresponding
// to the given built-in definition.
func NewFunctionDefinition(
	name string, props *FunctionProperties, def []Overload,
) *FunctionDefinition {
	overloads := make([]*Overload, len(def))

	for i := range def {
		if def[i].PreferredOverload {
			// Builtins with a preferred overload are always ambiguous.
			props.AmbiguousReturnType = true
		}

		def[i].FunctionProperties = *props
		overloads[i] = &def[i]
	}
	return &FunctionDefinition{
		Name:               name,
		Definition:         overloads,
		FunctionProperties: *props,
	}
}

// PrefixBuiltinFunctionDefinition prefix all overloads in a function definition
// with a schema name. The returned ResolvedFunctionDefinition is considered
// having ExplicitSchema. Note that this function can only be used for builtin
// function. Hence, HasUDF is set to false.
func PrefixBuiltinFunctionDefinition(
	def *FunctionDefinition, schema string,
) *ResolvedFunctionDefinition {
	ret := &ResolvedFunctionDefinition{
		Name:           def.Name,
		ExplicitSchema: true,
		Overloads:      make([]*PrefixedOverload, 0, len(def.Definition)),
	}
	for _, o := range def.Definition {
		ret.Overloads = append(
			ret.Overloads,
			&PrefixedOverload{Schema: schema, Overload: o},
		)
	}
	return ret
}

// FunDefs holds pre-allocated FunctionDefinition instances
// for every builtin function. Initialized by builtins.init().
//
// Note that this is extremely similar to the set stored in builtinsregistry.
// The hope is to remove this map at some point in the future as we delegate
// function definition resolution to interfaces defined in the SemaContext.
var FunDefs map[string]*FunctionDefinition

// OidToBuiltinName contains a map from the hashed OID of all builtin functions
// to their name. We populate this from the pg_catalog.go file in the sql
// package because of dependency issues: we can't use oidHasher from this file.
var OidToBuiltinName map[oid.Oid]string

// Format implements the NodeFormatter interface.
func (fd *FunctionDefinition) Format(ctx *FmtCtx) {
	ctx.WriteString(fd.Name)
}

// String implements the Stringer interface.
func (fd *FunctionDefinition) String() string { return AsString(fd) }

// TODO(Chengxiong): Remove this method after we moved the
// "UnsupportedWithIssue" check into function resolver implementation.
func (fd *FunctionDefinition) undefined() bool {
	return fd.UnsupportedWithIssue != 0
}

// GetClass returns function class by checking each overload's Class and returns
// the homogeneous Class value if all overloads are the same Class. Ambiguous
// error is returned if there is any overload with different Class.
func (fd *FunctionDefinition) GetClass() (FunctionClass, error) {
	if fd.undefined() {
		return fd.Class, nil
	}
	return getFuncClass(fd.Name, fd.Definition)
}

// GetReturnLabel returns function ReturnLabel by checking each overload and
// returns a ReturnLabel if all overloads have a ReturnLabel of the same length.
// Ambiguous error is returned if there is any overload has ReturnLabel of a
// different length. This is good enough since we don't create UDF with
// ReturnLabel.
func (fd *FunctionDefinition) GetReturnLabel() ([]string, error) {
	if fd.undefined() {
		return fd.ReturnLabels, nil
	}
	return getFuncReturnLabels(fd.Name, fd.Definition)
}

// GetHasSequenceArguments returns function's HasSequenceArguments flag by
// checking each overload's HasSequenceArguments flag. Ambiguous error is
// returned if there is any overload has a different flag.
func (fd *FunctionDefinition) GetHasSequenceArguments() (bool, error) {
	if fd.undefined() {
		return fd.HasSequenceArguments, nil
	}
	return getHasSequenceArguments(fd.Name, fd.Definition)
}

func (fd *ResolvedFunctionDefinition) GetClass() (FunctionClass, error) {
	return getFuncClass(fd.Name, toOverloads(fd.Overloads))
}

func (fd *ResolvedFunctionDefinition) GetReturnLabel() ([]string, error) {
	return getFuncReturnLabels(fd.Name, toOverloads(fd.Overloads))
}

func (fd *ResolvedFunctionDefinition) GetHasSequenceArguments() (bool, error) {
	return getHasSequenceArguments(fd.Name, toOverloads(fd.Overloads))
}

func toOverloads(in []*PrefixedOverload) []*Overload {
	ret := make([]*Overload, len(in), len(in))
	for i := range in {
		ret[i] = in[i].Overload
	}
	return ret
}

func getFuncClass(fnName string, fns []*Overload) (FunctionClass, error) {
	ret := fns[0].Class
	for _, o := range fns {
		if o.Class != ret {
			return 0, pgerror.Newf(pgcode.AmbiguousFunction, "ambiguous function class on %s", fnName)
		}
	}
	return ret, nil
}

func getFuncReturnLabels(fnName string, fns []*Overload) ([]string, error) {
	ret := fns[0].ReturnLabels
	for _, o := range fns {
		if len(ret) != len(o.ReturnLabels) {
			return nil, pgerror.Newf(pgcode.AmbiguousFunction, "ambiguous function return label on %s", fnName)
		}
	}
	return ret, nil
}

func getHasSequenceArguments(fnName string, fns []*Overload) (bool, error) {
	ret := fns[0].HasSequenceArguments
	for _, o := range fns {
		if ret != o.HasSequenceArguments {
			return false, pgerror.Newf(pgcode.AmbiguousFunction, "ambiguous function sequence argument on %s", fnName)
		}
	}
	return ret, nil
}

// GetBuiltinFuncDefinitionOrFail is similar to GetBuiltinFuncDefinition but
// fail if function is not found.
func GetBuiltinFuncDefinitionOrFail(
	fName *FunctionName, searchPath SearchPath,
) (*ResolvedFunctionDefinition, error) {
	def, err := GetBuiltinFuncDefinition(fName, searchPath)
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, pgerror.Newf(pgcode.UndefinedFunction, "unknown function %s", fName.String())
	}
	return def, nil
}

// GetBuiltinFuncDefinition search for a builtin function given a function name
// and a search path. If function name is prefixed, only the builtin functions
// in the specific schema are searched. Otherwise, all schemas on the given
// searchPath are searched. A nil is returned if no function is found. It's
// caller's choice to error out if function not found.
func GetBuiltinFuncDefinition(
	fName *FunctionName, searchPath SearchPath,
) (*ResolvedFunctionDefinition, error) {
	if fName.ExplicitSchema {
		// We only look at builtin functions with "AvailableOnPublicSchema == true"
		// when public schema is specified.
		if fName.Schema() == catconstants.PublicSchemaName {
			d := FunDefs[fName.Object()]
			if d != nil && d.AvailableOnPublicSchema {
				return PrefixBuiltinFunctionDefinition(d, fName.Object()), nil
			}
			return nil, nil
		}

		fullName := fName.Object()
		// If it's specified schema is not "pg_catalog", prefix the function name with
		// the schema name. For example, for functions in "crdb_internal" schema,
		// "crdb_internal" schema name need to be specified to resolve functions.
		if fName.Schema() != catconstants.PgCatalogName {
			fullName = fName.Schema() + "." + fullName
		}

		if d := FunDefs[fullName]; d != nil {
			return PrefixBuiltinFunctionDefinition(d, fName.Object()), nil
		}

		return nil, nil
	}

	def := FunDefs[fName.Object()]
	if def != nil {
		// If function is found with only the function, then it's a "pg_catalog"
		// builtin.
		return PrefixBuiltinFunctionDefinition(def, catconstants.PgCatalogName), nil
	}

	var resolvedDef *ResolvedFunctionDefinition
	if err := searchPath.IterateSearchPath(func(schema string) error {
		fullName := schema + "." + fName.Object()
		if def = FunDefs[fullName]; def != nil {
			resolvedDef = PrefixBuiltinFunctionDefinition(def, schema)
			return iterutil.StopIteration()
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return resolvedDef, nil
}
