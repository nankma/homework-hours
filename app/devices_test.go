package main

import "testing"

func TestMergeDevicesReviewed(t *testing.T) {
	seen := func(mac string) bool { return mac != "aa:aa:aa:aa:aa:03" }
	got := mergeDevices(nil, []Device{
		{MAC: "AA:AA:AA:AA:AA:01", Reviewed: true}, // kept: reviewed only
		{MAC: "aa:aa:aa:aa:aa:02"},                 // dropped: nothing to store
		{MAC: "aa:aa:aa:aa:aa:03", Reviewed: true}, // dropped: never seen
	}, nil, seen)
	if len(got) != 1 || got[0].MAC != "aa:aa:aa:aa:aa:01" || !got[0].Reviewed {
		t.Errorf("merge: %+v", got)
	}
}
