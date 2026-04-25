package db

import (
	"testing"
)

func TestCloudSyncDenyList_Matches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path         string
		wantHit      bool
		wantProvider string
	}{
		{"/Users/alec/Dropbox/data.db", true, "dropbox"},
		{"/Users/alec/Dropbox (Personal)/data.db", true, "dropbox"},
		{"/Users/alec/Google Drive/My Drive/data.db", true, "google-drive"},
		{"/Users/alec/My Drive/foo/data.db", true, "google-drive"},
		{"/Users/alec/OneDrive/data.db", true, "onedrive"},
		{"/Users/alec/OneDrive - Acme/data.db", true, "onedrive"},
		{"/Users/alec/Insync/foo@bar.com/data.db", true, "insync"},
		{"/Users/alec/Documents/data.db", false, ""},
		{"/var/lib/harness/data.db", false, ""},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			hit, provider, _ := matchCloudSyncRoot(c.path)
			if hit != c.wantHit {
				t.Errorf("hit = %v, want %v", hit, c.wantHit)
			}
			if c.wantHit && provider != c.wantProvider {
				t.Errorf("provider = %q, want %q", provider, c.wantProvider)
			}
		})
	}
}

func TestMountReport_IsLocal(t *testing.T) {
	t.Parallel()
	if !(MountReport{Kind: MountKindLocal}).IsLocal() {
		t.Error("local should be local")
	}
	if (MountReport{Kind: MountKindNFS}).IsLocal() {
		t.Error("NFS should not be local")
	}
}
