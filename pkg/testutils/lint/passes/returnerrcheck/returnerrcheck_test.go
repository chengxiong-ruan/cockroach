// Copyright 2019 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package returnerrcheck_test

import (
	"testing"

	"github.com/cockroachdb/cockroach/pkg/testutils/lint/passes/returnerrcheck"
	"github.com/cockroachdb/cockroach/pkg/testutils/skip"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	skip.UnderStress(t, "Go cache files don't work under stress")
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, returnerrcheck.Analyzer, "a")
}
