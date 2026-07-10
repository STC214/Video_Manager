package archive

import "testing"

func TestCalculateCapacityThreeLevels(t *testing.T) {
	result := CalculateCapacity(CapacityConfig{
		TotalFiles:      7429,
		LevelCount:      3,
		LevelNames:      []string{"Arc", "Season", "Episode"},
		FoldersPerLevel: []int{5, 10, 5},
		FilesPerLeaf:    30,
	})

	if result.RequiredLeafDirs != 248 {
		t.Fatalf("RequiredLeafDirs = %d, want 248", result.RequiredLeafDirs)
	}
	if result.LastLeafPath != "Arc_05/Season_050/Episode_248" {
		t.Fatalf("LastLeafPath = %q", result.LastLeafPath)
	}
	if result.LastLeafFileCount != 19 {
		t.Fatalf("LastLeafFileCount = %d, want 19", result.LastLeafFileCount)
	}
	if result.MaxCapacity != 7500 {
		t.Fatalf("MaxCapacity = %d, want 7500", result.MaxCapacity)
	}
	if !result.Enough {
		t.Fatal("expected capacity to be enough")
	}

	want := []int{5, 50, 248}
	for i := range want {
		if result.ActualDirsPerLevel[i] != want[i] {
			t.Fatalf("ActualDirsPerLevel[%d] = %d, want %d", i, result.ActualDirsPerLevel[i], want[i])
		}
	}
}

func TestCalculateCapacityInsufficient(t *testing.T) {
	result := CalculateCapacity(CapacityConfig{
		TotalFiles:      1000,
		LevelCount:      2,
		LevelNames:      []string{"Season", "Episode"},
		FoldersPerLevel: []int{2, 10},
		FilesPerLeaf:    30,
	})

	if result.Enough {
		t.Fatal("expected capacity to be insufficient")
	}
	if result.MaxCapacity != 600 {
		t.Fatalf("MaxCapacity = %d, want 600", result.MaxCapacity)
	}
	if result.MissingCapacity != 400 {
		t.Fatalf("MissingCapacity = %d, want 400", result.MissingCapacity)
	}
}

func TestCalculateCapacitySaturatesLargeConfiguration(t *testing.T) {
	result := CalculateCapacity(CapacityConfig{
		TotalFiles:      1_000_000_000,
		LevelCount:      5,
		LevelNames:      []string{"A", "B", "C", "D", "E"},
		FoldersPerLevel: []int{1_000_000, 1_000_000, 1_000_000, 1_000_000, 1_000_000},
		FilesPerLeaf:    1_000_000,
	})

	if result.MaxCapacity <= 0 {
		t.Fatalf("MaxCapacity overflowed: %d", result.MaxCapacity)
	}
	if !result.Enough {
		t.Fatal("large saturated capacity should be enough")
	}
}
