package main

import (
	"path/filepath"
	"testing"
)

func TestPetExecutableCandidatesIncludeStandaloneBuildLayout(t *testing.T) {
	current := filepath.Join(`C:\workspace`, "bin", "CodeSwitch.exe")
	cwd := `C:\workspace`
	candidates := petExecutableCandidates(current, cwd)
	want := filepath.Join(`C:\workspace`, "bin", "pet", "CodeSwitchPet.exe")
	for _, candidate := range candidates {
		if filepath.Clean(candidate) == filepath.Clean(want) {
			return
		}
	}
	t.Fatalf("petExecutableCandidates() = %#v, want %q", candidates, want)
}
