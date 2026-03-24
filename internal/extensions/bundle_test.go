package extensions

import "testing"

func TestAssetSpecForSupportedPlatforms(t *testing.T) {
	cases := []struct {
		name     string
		extName  string
		goos     string
		goarch   string
		fileName string
		entry    string
	}{
		{name: "darwin arm64 crsqlite", extName: NameCRSQLite, goos: "darwin", goarch: "arm64", fileName: "crsqlite.dylib", entry: "assets/darwin-arm64/crsqlite.dylib"},
		{name: "darwin amd64 vec", extName: NameSQLiteVec, goos: "darwin", goarch: "amd64", fileName: "vec0.dylib", entry: "assets/darwin-amd64/vec0.dylib"},
		{name: "linux amd64 crsqlite", extName: NameCRSQLite, goos: "linux", goarch: "amd64", fileName: "crsqlite.so", entry: "assets/linux-amd64/crsqlite.so"},
		{name: "linux arm64 vec", extName: NameSQLiteVec, goos: "linux", goarch: "arm64", fileName: "vec0.so", entry: "assets/linux-arm64/vec0.so"},
		{name: "windows amd64 crsqlite", extName: NameCRSQLite, goos: "windows", goarch: "amd64", fileName: "crsqlite.dll", entry: "assets/windows-amd64/crsqlite.dll"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fileName, entry, err := assetSpecForPlatform(tc.extName, tc.goos, tc.goarch)
			if err != nil {
				t.Fatal(err)
			}
			if fileName != tc.fileName {
				t.Fatalf("fileName = %q, want %q", fileName, tc.fileName)
			}
			if entry != tc.entry {
				t.Fatalf("entry = %q, want %q", entry, tc.entry)
			}
		})
	}
}

func TestAssetSpecForUnsupportedPlatform(t *testing.T) {
	if _, _, err := assetSpecForPlatform(NameCRSQLite, "windows", "arm64"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}
