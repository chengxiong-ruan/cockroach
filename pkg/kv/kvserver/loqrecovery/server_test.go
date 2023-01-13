// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package loqrecovery_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/kv/kvserver/loqrecovery"
	"github.com/cockroachdb/cockroach/pkg/kv/kvserver/loqrecovery/loqrecoverypb"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/server"
	"github.com/cockroachdb/cockroach/pkg/server/serverpb"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/testcluster"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/uuid"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

type clusterInfoCounters struct {
	nodes, stores, replicas, descriptors int
}

func TestReplicaCollection(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()

	tc := testcluster.NewTestCluster(t, 3, base.TestClusterArgs{
		ServerArgs: base.TestServerArgs{
			StoreSpecs: []base.StoreSpec{{InMemory: true}},
			Insecure:   true,
			Knobs: base.TestingKnobs{
				LOQRecovery: &loqrecovery.TestingKnobs{
					MetadataScanTimeout: 15 * time.Second,
				},
			},
		},
	})
	tc.Start(t)
	defer tc.Stopper().Stop(ctx)
	require.NoError(t, tc.WaitForFullReplication())
	tc.ToggleReplicateQueues(false)

	r := tc.ServerConn(0).QueryRow("select count(*) from crdb_internal.ranges_no_leases")
	var totalRanges int
	require.NoError(t, r.Scan(&totalRanges), "failed to query range count")
	adm, err := tc.GetAdminClient(ctx, t, 2)
	require.NoError(t, err, "failed to get admin client")

	// Collect and assert replica metadata. For expectMeta case we sometimes have
	// meta and sometimes doesn't depending on which node holds the lease.
	// We just ignore descriptor counts if we are not expecting meta.
	assertReplicas := func(liveNodes int, expectMeta bool) {
		var replicas loqrecoverypb.ClusterReplicaInfo
		var stats loqrecovery.CollectionStats

		replicas, stats, err = loqrecovery.CollectRemoteReplicaInfo(ctx, adm)
		require.NoError(t, err, "failed to retrieve replica info")

		// Check counters on retrieved replica info.
		cnt := getInfoCounters(replicas)
		require.Equal(t, liveNodes, cnt.stores, "collected replicas from stores")
		require.Equal(t, liveNodes, cnt.nodes, "collected replicas from nodes")
		if expectMeta {
			require.Equal(t, totalRanges, cnt.descriptors,
				"number of collected descriptors from metadata")
		}
		require.Equal(t, totalRanges*liveNodes, cnt.replicas, "number of collected replicas")
		// Check stats counters as well.
		require.Equal(t, liveNodes, stats.Nodes, "node counter stats")
		require.Equal(t, liveNodes, stats.Stores, "store counter stats")
		if expectMeta {
			require.Equal(t, totalRanges, stats.Descriptors, "range descriptor counter stats")
		}
		require.NotEqual(t, replicas.ClusterID, uuid.UUID{}.String(), "cluster UUID must not be empty")
	}

	tc.StopServer(0)
	assertReplicas(2, true)
	tc.StopServer(1)
	assertReplicas(1, false)

	tc.Stopper().Stop(ctx)
}

// TestStreamRestart verifies that if connection is dropped mid way through
// replica stream, it would be handled correctly with a stream restart that
// allows caller to rewind back partial replica data and receive consistent
// stream of replcia infos.
func TestStreamRestart(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()

	var failCount atomic.Int64
	tc := testcluster.NewTestCluster(t, 3, base.TestClusterArgs{
		ServerArgs: base.TestServerArgs{
			StoreSpecs: []base.StoreSpec{{InMemory: true}},
			Insecure:   true,
			Knobs: base.TestingKnobs{
				LOQRecovery: &loqrecovery.TestingKnobs{
					MetadataScanTimeout: 15 * time.Second,
					ForwardReplicaFilter: func(response *serverpb.RecoveryCollectLocalReplicaInfoResponse) error {
						if response.ReplicaInfo.NodeID == 2 && response.ReplicaInfo.Desc.RangeID == 14 && failCount.Add(1) < 3 {
							return errors.New("rpc stream stopped")
						}
						return nil
					},
				},
			},
		},
	})
	tc.Start(t)
	defer tc.Stopper().Stop(ctx)
	require.NoError(t, tc.WaitForFullReplication())
	tc.ToggleReplicateQueues(false)

	r := tc.ServerConn(0).QueryRow("select count(*) from crdb_internal.ranges_no_leases")
	var totalRanges int
	require.NoError(t, r.Scan(&totalRanges), "failed to query range count")
	adm, err := tc.GetAdminClient(ctx, t, 2)
	require.NoError(t, err, "failed to get admin client")

	assertReplicas := func(liveNodes int) {
		var replicas loqrecoverypb.ClusterReplicaInfo
		var stats loqrecovery.CollectionStats

		replicas, stats, err = loqrecovery.CollectRemoteReplicaInfo(ctx, adm)
		require.NoError(t, err, "failed to retrieve replica info")

		// Check counters on retrieved replica info.
		cnt := getInfoCounters(replicas)
		require.Equal(t, liveNodes, cnt.stores, "collected replicas from stores")
		require.Equal(t, liveNodes, cnt.nodes, "collected replicas from nodes")
		require.Equal(t, totalRanges, cnt.descriptors,
			"number of collected descriptors from metadata")
		require.Equal(t, totalRanges*liveNodes, cnt.replicas,
			"number of collected replicas")
		// Check stats counters as well.
		require.Equal(t, liveNodes, stats.Nodes, "node counter stats")
		require.Equal(t, liveNodes, stats.Stores, "store counter stats")
		require.Equal(t, totalRanges, stats.Descriptors, "range descriptor counter stats")
	}

	assertReplicas(3)

	tc.Stopper().Stop(ctx)
}

func getInfoCounters(info loqrecoverypb.ClusterReplicaInfo) clusterInfoCounters {
	stores := map[roachpb.StoreID]interface{}{}
	nodes := map[roachpb.NodeID]interface{}{}
	totalReplicas := 0
	for _, nr := range info.LocalInfo {
		for _, r := range nr.Replicas {
			stores[r.StoreID] = struct{}{}
			nodes[r.NodeID] = struct{}{}
		}
		totalReplicas += len(nr.Replicas)
	}
	return clusterInfoCounters{
		nodes:       len(nodes),
		stores:      len(stores),
		replicas:    totalReplicas,
		descriptors: len(info.Descriptors),
	}
}

func TestGetRecoveryState(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()

	reg := server.NewStickyInMemEnginesRegistry()
	defer reg.CloseAllStickyInMemEngines()

	args := base.TestClusterArgs{
		ServerArgsPerNode: make(map[int]base.TestServerArgs),
	}
	for i := 0; i < 3; i++ {
		args.ServerArgsPerNode[i] = base.TestServerArgs{
			Knobs: base.TestingKnobs{
				Server: &server.TestingKnobs{
					StickyEngineRegistry: reg,
				},
			},
			StoreSpecs: []base.StoreSpec{
				{
					InMemory:               true,
					StickyInMemoryEngineID: strconv.FormatInt(int64(i), 10),
				},
			},
		}
	}
	tc := testcluster.NewTestCluster(t, 3, args)
	tc.Start(t)
	defer tc.Stopper().Stop(ctx)

	planStores := prepInMemPlanStores(t, args.ServerArgsPerNode)

	adm, err := tc.GetAdminClient(ctx, t, 0)
	require.NoError(t, err, "failed to get admin client")

	resp, err := adm.RecoveryVerify(ctx, &serverpb.RecoveryVerifyRequest{})
	require.NoError(t, err)
	for _, s := range resp.Statuses {
		require.Nil(t, s.PendingPlanID, "no pending plan")
	}

	// Injecting plan into 2 nodes out of 3.
	plan := loqrecoverypb.ReplicaUpdatePlan{
		PlanID: uuid.MakeV4(),
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, planStores[i].SavePlan(plan), "failed to save plan on node n%d", i)
	}

	// First we test that plans are successfully picked up by status call.
	resp, err = adm.RecoveryVerify(ctx, &serverpb.RecoveryVerifyRequest{})
	require.NoError(t, err)
	statuses := aggregateStatusByNode(resp)
	require.Equal(t, &plan.PlanID, statuses[1].PendingPlanID, "incorrect plan id on node 0")
	require.Equal(t, &plan.PlanID, statuses[2].PendingPlanID, "incorrect plan id on node 1")
	require.Nil(t, statuses[3].PendingPlanID, "unexpected plan id on node 2")

	// Check we can collect partial results.
	tc.StopServer(1)

	testutils.SucceedsSoon(t, func() error {
		resp, err = adm.RecoveryVerify(ctx, &serverpb.RecoveryVerifyRequest{})
		if err != nil {
			return err
		}
		if len(resp.Statuses) > 2 {
			return errors.New("too many statuses in response")
		}
		return nil
	})

	statuses = aggregateStatusByNode(resp)
	require.Equal(t, &plan.PlanID, statuses[1].PendingPlanID, "incorrect plan id")
	require.Nil(t, statuses[3].PendingPlanID, "unexpected plan id")
}

func aggregateStatusByNode(
	resp *serverpb.RecoveryVerifyResponse,
) map[roachpb.NodeID]loqrecoverypb.NodeRecoveryStatus {
	statuses := make(map[roachpb.NodeID]loqrecoverypb.NodeRecoveryStatus)
	for _, s := range resp.Statuses {
		statuses[s.NodeID] = s
	}
	return statuses
}

func prepInMemPlanStores(
	t *testing.T, serverArgs map[int]base.TestServerArgs,
) map[int]loqrecovery.PlanStore {
	pss := make(map[int]loqrecovery.PlanStore)
	for id, args := range serverArgs {
		reg := args.Knobs.Server.(*server.TestingKnobs).StickyEngineRegistry
		store, err := reg.GetUnderlyingFS(args.StoreSpecs[0])
		require.NoError(t, err, "can't create loq recovery plan store")
		pss[id] = loqrecovery.NewPlanStore(".", store)
	}
	return pss
}
