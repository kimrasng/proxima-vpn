package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// These values come from authenticated GitHub API responses, not release
// assets or fields supplied by the build. The workflow supplies the expected
// commit from its own trusted dispatch context and resolves the tag to it.
type buildIdentity struct {
	repository string
	tag        string
	sha        string
	runID      int64
}

type workflowRun struct {
	ID         int64  `json:"id"`
	RunAttempt int    `json:"run_attempt"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Path       string `json:"path"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	HeadRepository struct {
		FullName string `json:"full_name"`
	} `json:"head_repository"`
}

type buildArtifact struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Expired     bool   `json:"expired"`
	Digest      string `json:"digest"`
	WorkflowRun struct {
		ID         int64  `json:"id"`
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
	} `json:"workflow_run"`
}

type digestRecord struct {
	Schema     int    `json:"schema"`
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	SHA        string `json:"sha"`
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
	API        string `json:"api"`
	Web        string `json:"web"`
}

func runVerifyBuild(args []string, stdout io.Writer) error {
	var expected buildIdentity
	var runPath, artifactPath, archivePath, out string
	fs := flag.NewFlagSet("verify-build", flag.ContinueOnError)
	fs.StringVar(&expected.repository, "repository", "", "trusted GitHub owner/repository")
	fs.StringVar(&expected.tag, "tag", "", "tag resolved to the trusted dispatch commit")
	fs.StringVar(&expected.sha, "sha", "", "trusted dispatch commit SHA")
	fs.Int64Var(&expected.runID, "run-id", 0, "tag-push workflow run ID")
	fs.StringVar(&runPath, "run", "", "authenticated workflow run API response")
	fs.StringVar(&artifactPath, "artifact", "", "authenticated artifact API response")
	fs.StringVar(&archivePath, "archive", "", "downloaded Actions artifact ZIP")
	fs.StringVar(&out, "out", "", "verified digest record output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || out == "" {
		return errors.New("verify-build: output required and positional arguments forbidden")
	}
	var run workflowRun
	var artifact buildArtifact
	runData, err := os.ReadFile(runPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(runData, &run); err != nil {
		return fmt.Errorf("GitHub run metadata: %w", err)
	}
	artifactData, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(artifactData, &artifact); err != nil {
		return fmt.Errorf("GitHub artifact metadata: %w", err)
	}
	if err := verifyBuildIdentity(expected, run, artifact); err != nil {
		return err
	}
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		return err
	}
	record, err := verifyDigestArchive(expected, artifact.Digest, archive)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(out, data); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "verified build run %d, artifact %d, commit %s\n", run.ID, artifact.ID, expected.sha)
	return nil
}

func verifyBuildIdentity(want buildIdentity, run workflowRun, artifact buildArtifact) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(want.repository) ||
		!regexp.MustCompile(`^v[0-9][A-Za-z0-9._-]*$`).MatchString(want.tag) ||
		!regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(want.sha) || want.runID <= 0 {
		return errors.New("invalid trusted build identity")
	}
	if run.ID != want.runID || run.RunAttempt != 1 || run.Event != "push" ||
		run.Status != "completed" || run.Conclusion != "success" ||
		run.Path != ".github/workflows/release.yml" || run.HeadBranch != want.tag ||
		run.HeadSHA != want.sha || run.Repository.FullName != want.repository ||
		run.HeadRepository.FullName != want.repository {
		return errors.New("build run does not match the trusted tag-push identity; reruns are not accepted")
	}
	if artifact.ID <= 0 || artifact.Name != "image-digests" || artifact.Expired ||
		artifact.WorkflowRun.ID != want.runID || artifact.WorkflowRun.HeadSHA != want.sha ||
		artifact.WorkflowRun.HeadBranch != want.tag {
		return errors.New("digest artifact does not belong to the verified build run")
	}
	if !strings.HasPrefix(artifact.Digest, "sha256:") || !digestHex.MatchString(strings.TrimPrefix(artifact.Digest, "sha256:")) {
		return errors.New("GitHub artifact has no valid SHA256 digest")
	}
	return nil
}

func verifyDigestArchive(want buildIdentity, digest string, archive []byte) (digestRecord, error) {
	var record digestRecord
	hash := sha256.Sum256(archive)
	if digest != "sha256:"+hex.EncodeToString(hash[:]) {
		return record, errors.New("artifact archive does not match the GitHub SHA256 digest")
	}
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return record, err
	}
	if len(z.File) != 1 || z.File[0].Name != "image-digests.json" ||
		!z.File[0].Mode().IsRegular() || z.File[0].UncompressedSize64 > 16384 {
		return record, errors.New("artifact must contain only a bounded regular image-digests.json")
	}
	f, err := z.File[0].Open()
	if err != nil {
		return record, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, 16385))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return record, errors.Join(readErr, closeErr)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, err
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return record, errors.New("trailing artifact data")
	}
	if record.Schema != 1 || record.Repository != want.repository || record.Ref != "refs/tags/"+want.tag ||
		record.SHA != want.sha || record.RunID != want.runID || record.RunAttempt != 1 {
		return record, errors.New("digest record does not match the verified build identity")
	}
	images, err := parseImages(record.API, record.Web)
	if err != nil {
		return record, err
	}
	base := "ghcr.io/" + strings.ToLower(want.repository)
	if repositoryOf(images.API) != base+"/api" || repositoryOf(images.Web) != base+"/web" {
		return record, errors.New("digest record image repositories do not match their expected roles")
	}
	return record, nil
}
