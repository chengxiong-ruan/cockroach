// Copyright 2017 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package sql

import "github.com/cockroachdb/cockroach/pkg/sql/catalog/colinfo"

// ReqOrdering is the ordering that must be preserved by an operator when it is
// distributed. It is used to configure DistSQL with the orderings it needs to
// maintain when joining streams.
type ReqOrdering = colinfo.ColumnOrdering

// planReqOrdering describes known ordering information for the rows generated by
// this node. The ordering information includes columns the output is ordered
// by and columns for which we know all rows have the same value.
func planReqOrdering(plan planNode) ReqOrdering {
	switch n := plan.(type) {
	case *limitNode:
		return planReqOrdering(n.plan)
	case *max1RowNode:
		return planReqOrdering(n.plan)
	case *spoolNode:
		return planReqOrdering(n.source)
	case *saveTableNode:
		return planReqOrdering(n.source)
	case *serializeNode:
		return planReqOrdering(n.source)
	case *deleteNode:
		if n.run.rowsNeeded {
			return planReqOrdering(n.source)
		}

	case *filterNode:
		return n.reqOrdering

	case *groupNode:
		return n.reqOrdering

	case *distinctNode:
		return n.reqOrdering

	case *indexJoinNode:
		return n.reqOrdering

	case *windowNode:
		// TODO: window partitions can be ordered if the source is ordered
		// appropriately.
	case *joinNode:
		return n.reqOrdering
	case *interleavedJoinNode:
		return n.reqOrdering
	case *unionNode:
		// TODO(knz): this can be ordered if the source is ordered already.
	case *insertNode, *insertFastPathNode:
		// TODO(knz): RETURNING is ordered by the PK.
	case *updateNode, *upsertNode:
		// After an update, the original order may have been destroyed.
		// For example, if the PK is updated by a SET expression.
		// So we can't assume any ordering.
		//
		// TODO(knz/radu): this can be refined by an analysis which
		// determines whether the columns that participate in the ordering
		// of the source are being updated. If they are not, the source
		// ordering can be propagated.

	case *scanNode:
		return n.reqOrdering
	case *ordinalityNode:
		return n.reqOrdering
	case *renderNode:
		return n.reqOrdering
	case *sortNode:
		return n.ordering
	case *lookupJoinNode:
		return n.reqOrdering
	case *invertedJoinNode:
		return n.reqOrdering
	case *zigzagJoinNode:
		return n.reqOrdering
	}

	return nil
}
