// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package fastdelta

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type locTest struct {
	ID          uint64
	Address     uint64
	Mapping     uint64
	FunctionIDs []uint64
}

func TestLocationIndex(t *testing.T) {
	var loc locationIndex

	tests := []locTest{
		{ID: 1, Address: 0x40, Mapping: 1, FunctionIDs: []uint64{1, 2, 3}},
		{ID: 2, Address: 0x41, Mapping: 2, FunctionIDs: []uint64{4, 2, 3}},
		{ID: 3, Address: 0x42, Mapping: 1, FunctionIDs: []uint64{1, 7, 3}},
		{ID: 6, Address: 0x43, Mapping: 2, FunctionIDs: []uint64{1, 2, 8}},
	}

	for _, l := range tests {
		loc.insert(l.ID, l.Address, l.Mapping, l.FunctionIDs)
		require.Equal(t, l.Address, loc.get(l.ID))
		mapping, functionIDs := loc.getMeta(l.ID)
		require.Equal(t, l.Mapping, mapping)
		require.Equal(t, l.FunctionIDs, functionIDs)
	}

	// Check that the original things are still valid
	for _, l := range tests {
		require.Equal(t, l.Address, loc.get(l.ID))
		mapping, functionIDs := loc.getMeta(l.ID)
		require.Equal(t, l.Mapping, mapping)
		require.Equal(t, l.FunctionIDs, functionIDs)
	}
}