package buildinfo

import "testing"

func TestCurrentPreservesBuildStamp(t *testing.T) {
	oldVersion, oldRepository, oldRevision, oldTime := Version, Repository, Revision, BuildTime
	t.Cleanup(func() { Version, Repository, Revision, BuildTime = oldVersion, oldRepository, oldRevision, oldTime })
	Version, Repository, Revision, BuildTime = "v3.2.1", "proxima/server", "abc123", "2026-11-04T00:00:00Z"
	i := Current()
	if i.Version != "v3.2.1" || i.Repository != "proxima/server" || i.Revision != "abc123" || i.BuildTime != "2026-11-04T00:00:00Z" {
		t.Fatalf("build stamp lost: %+v", i)
	}
}

func TestCurrentDoesNotInferReleaseVersion(t *testing.T) {
	i := Current()
	if i.Version != "dev" || i.Repository != "" {
		t.Fatalf("unstamped build claimed release identity: %+v", i)
	}
}
