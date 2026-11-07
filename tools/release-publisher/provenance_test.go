package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyBuildBindsOutputToAuthenticatedRunAndArchive(t *testing.T) {
	want := buildIdentity{"example/proxima-vpn", "v1.2.3", strings.Repeat("a", 40), 42}
	for _, tc := range []struct {
		name string
		edit func(*workflowRun, *buildArtifact, *digestRecord)
	}{
		{"valid", func(*workflowRun, *buildArtifact, *digestRecord) {}},
		{"different run", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.ID++ }},
		{"manual run", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.Event = "workflow_dispatch" }},
		{"rerun", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.RunAttempt++ }},
		{"incomplete run", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.Status = "in_progress" }},
		{"failed run", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.Conclusion = "failure" }},
		{"different workflow", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.Path = ".github/workflows/other.yml" }},
		{"different tag", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.HeadBranch = "v9" }},
		{"different commit", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.HeadSHA = strings.Repeat("b", 40) }},
		{"different repository", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.Repository.FullName = "other/repo" }},
		{"fork head", func(r *workflowRun, _ *buildArtifact, _ *digestRecord) { r.HeadRepository.FullName = "fork/repo" }},
		{"expired artifact", func(_ *workflowRun, a *buildArtifact, _ *digestRecord) { a.Expired = true }},
		{"missing artifact", func(_ *workflowRun, a *buildArtifact, _ *digestRecord) { a.ID = 0 }},
		{"wrong artifact name", func(_ *workflowRun, a *buildArtifact, _ *digestRecord) { a.Name = "other" }},
		{"wrong artifact run", func(_ *workflowRun, a *buildArtifact, _ *digestRecord) { a.WorkflowRun.ID++ }},
		{"wrong artifact commit", func(_ *workflowRun, a *buildArtifact, _ *digestRecord) { a.WorkflowRun.HeadSHA = "" }},
		{"wrong artifact branch", func(_ *workflowRun, a *buildArtifact, _ *digestRecord) { a.WorkflowRun.HeadBranch = "main" }},
		{"missing digest", func(_ *workflowRun, a *buildArtifact, _ *digestRecord) { a.Digest = "" }},
		{"mismatched digest", func(_ *workflowRun, a *buildArtifact, _ *digestRecord) { a.Digest = "sha256:" + hex64('0') }},
		{"record schema", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.Schema++ }},
		{"record repository", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.Repository = "other/repo" }},
		{"record ref", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.Ref = "refs/heads/main" }},
		{"record sha", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.SHA = strings.Repeat("c", 40) }},
		{"record run", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.RunID++ }},
		{"record attempt", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.RunAttempt++ }},
		{"swapped roles", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.API, d.Web = d.Web, d.API }},
		{"mutable target", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.API = apiRepo + ":latest" }},
		{"foreign target", func(_ *workflowRun, _ *buildArtifact, d *digestRecord) { d.Web = ref("ghcr.io/other/web", hex64('d')) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := workflowRun{ID: 42, RunAttempt: 1, Event: "push", Status: "completed", Conclusion: "success",
				Path: ".github/workflows/release.yml", HeadBranch: want.tag, HeadSHA: want.sha}
			run.Repository.FullName, run.HeadRepository.FullName = want.repository, want.repository
			artifact := buildArtifact{ID: 9, Name: "image-digests", Digest: "compute-from-fixture"}
			artifact.WorkflowRun.ID, artifact.WorkflowRun.HeadBranch, artifact.WorkflowRun.HeadSHA = 42, want.tag, want.sha
			record := digestRecord{1, want.repository, "refs/tags/" + want.tag, want.sha, 42, 1,
				ref(apiRepo, hex64('c')), ref(webRepo, hex64('d'))}
			// Mutate the record before hashing: invalid metadata must be rejected
			// even when the archive has an authentic, matching GitHub digest.
			tc.edit(&run, &artifact, &record)
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			archive := digestZip(t, "image-digests.json", data)
			hash := sha256.Sum256(archive)
			if artifact.Digest == "compute-from-fixture" {
				artifact.Digest = "sha256:" + hex.EncodeToString(hash[:])
			}
			dir := t.TempDir()
			runPath, artifactPath, archivePath := filepath.Join(dir, "run"), filepath.Join(dir, "artifact"), filepath.Join(dir, "zip")
			writeJSON(t, runPath, run)
			writeJSON(t, artifactPath, artifact)
			if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "verified.json")
			err = runVerifyBuild([]string{"--repository", want.repository, "--tag", want.tag, "--sha", want.sha,
				"--run-id", "42", "--run", runPath, "--artifact", artifactPath, "--archive", archivePath, "--out", out}, &strings.Builder{})
			if tc.name != "valid" {
				if err == nil {
					t.Fatal("untrusted provenance accepted")
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("untrusted provenance produced signing input")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			verified, err := os.ReadFile(out)
			if err != nil || !bytes.Equal(verified, data) {
				t.Fatalf("verified record differs: %s, %v", verified, err)
			}
		})
	}
}

func digestZip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeJSON[T any](t *testing.T, path string, value T) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDigestArchiveRejectsUnsafeOrMalformedPayload(t *testing.T) {
	want := buildIdentity{"example/proxima-vpn", "v1", strings.Repeat("a", 40), 42}
	for _, tc := range []struct{ name, path, data string }{
		{"path traversal", "../image-digests.json", `{}`},
		{"wrong name", "other.json", `{}`},
		{"oversized", "image-digests.json", strings.Repeat("x", 16385)},
		{"malformed", "image-digests.json", `{`},
		{"unknown field", "image-digests.json", `{"unexpected":1}`},
		{"trailing", "image-digests.json", `{} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := digestZip(t, tc.path, []byte(tc.data))
			hash := sha256.Sum256(archive)
			if _, err := verifyDigestArchive(want, "sha256:"+hex.EncodeToString(hash[:]), archive); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
