package main

import (
	"bytes"
	"testing"
)

func TestKeygenSeedDeterministicModeUsesPeerID(t *testing.T) {
	seedA, err := keygenSeed("peer-a", true)
	if err != nil {
		t.Fatal(err)
	}
	seedB, err := keygenSeed("peer-a", true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(seedA, seedB) {
		t.Fatal("deterministic keygen should return the same seed for the same peer")
	}
}

func TestKeygenSeedDefaultModeIsRandom(t *testing.T) {
	seedA, err := keygenSeed("peer-a", false)
	if err != nil {
		t.Fatal(err)
	}
	seedB, err := keygenSeed("peer-a", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(seedA) != 32 || len(seedB) != 32 {
		t.Fatalf("unexpected seed lengths: %d, %d", len(seedA), len(seedB))
	}
	if bytes.Equal(seedA, seedB) {
		t.Fatal("random keygen returned identical seeds")
	}
}
