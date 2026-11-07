package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/proximavpn/proxima-vpn/tools/server-updater/releaseverify"
)

const (
	product = "proxima-vpn"

	// Mirrors the v1 limits the verifier enforces (releaseverify/manifest.go):
	// a real transition needs sequence > high-water and high-water starts at 1,
	// and the validity window may not exceed 7 days.
	minSequence = 2
	maxValidity = 7 * 24 * time.Hour

	// The verifier has no clock-skew allowance: an envelope issued "in the
	// future" relative to a host is rejected until the host clock catches up.
	// Issuing slightly in the past keeps that failure mode off the host while
	// expiry still bounds the freeze window; it does not extend validity.
	issueBackdate = 5 * time.Minute
)

var (
	channelPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	releaseIDRegex  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	digestHex       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	errInvalidImage = errors.New("invalid image reference: want <repository>@sha256:<64 lowercase hex>")
)

// signOptions carries every operator decision. Nothing has a default that
// could silently weaken a release: channel, sequence, validity, both image
// pairs and the output path are all explicit inputs.
type signOptions struct {
	fromAPI, fromWeb, toAPI, toWeb string
	tag                            string
	releaseID                      string
	channel                        string
	sequence                       uint64
	validity                       time.Duration
	rollbackCompatible             bool
	out                            string
}

// runSign builds the v1 manifest from validated operator input, signs it with
// the environment key, re-verifies the exact envelope bytes with the real
// releaseverify.Verifier, and only then writes the output atomically. Any
// failure anywhere in that chain leaves no output file behind: the tool fails
// closed.
func runSign(args []string, stdout io.Writer) error {
	var o signOptions
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.StringVar(&o.fromAPI, "from-api", "", "current API reference: <repository>@sha256:<64 lowercase hex>")
	fs.StringVar(&o.fromWeb, "from-web", "", "current web reference: <repository>@sha256:<64 lowercase hex>")
	fs.StringVar(&o.toAPI, "to-api", "", "target API reference, from the recorded build digests")
	fs.StringVar(&o.toWeb, "to-web", "", "target web reference, from the recorded build digests")
	fs.StringVar(&o.tag, "tag", "", "release tag; used to derive the release ID when --release-id is absent")
	fs.StringVar(&o.releaseID, "release-id", "", "globally unique release ID (default: derived from tag and sequence)")
	fs.StringVar(&o.channel, "channel", "", "release channel; must equal the verifier policy channel")
	fs.Uint64Var(&o.sequence, "sequence", 0, "monotonic release sequence, allocated above every previously published envelope")
	fs.DurationVar(&o.validity, "validity", 0, "envelope validity window, e.g. 72h (positive, at most 168h)")
	fs.BoolVar(&o.rollbackCompatible, "rollback-compatible", false, "mark the transition rollback compatible")
	fs.StringVar(&o.out, "out", "", "output envelope path, written atomically with no trailing newline")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("sign: unexpected positional arguments")
	}
	key, err := loadSigningKey()
	if err != nil {
		return err
	}
	now := time.Now()
	m, err := buildManifest(o, now)
	if err != nil {
		return err
	}
	envelope, err := signEnvelope(key, m)
	if err != nil {
		return err
	}
	// Sign-then-verify with the actual verifier library: this binds the
	// publisher to releaseverify's exact canonical encoding, policy and
	// replay rules before anything is published. An envelope the updater
	// host would refuse is never written.
	if err := selfVerify(key, m, envelope, now); err != nil {
		return err
	}
	if err := atomicWriteFile(o.out, envelope); err != nil {
		return err
	}
	digest := sha256.Sum256(envelope)
	fmt.Fprintf(stdout, "signed release %s (sequence %d, channel %s)\n", m.ReleaseID, m.Sequence, m.Channel)
	fmt.Fprintf(stdout, "issued %s\nexpires %s\n",
		time.Unix(m.IssuedAt, 0).UTC().Format(time.RFC3339),
		time.Unix(m.ExpiresAt, 0).UTC().Format(time.RFC3339))
	fmt.Fprintf(stdout, "from api %s\nfrom web %s\nto api %s\nto web %s\n",
		m.From.API, m.From.Web, m.To.API, m.To.Web)
	fmt.Fprintf(stdout, "rollback compatible: %v\n", m.RollbackCompatible)
	fmt.Fprintf(stdout, "envelope %s (sha256:%s)\n", o.out, hex.EncodeToString(digest[:]))
	return nil
}

func buildManifest(o signOptions, now time.Time) (releaseverify.Manifest, error) {
	m := releaseverify.Manifest{}
	if o.sequence < minSequence || o.sequence > releaseverify.MaxSequence {
		return m, fmt.Errorf("invalid sequence %d: hosts keep a durable high-water that starts at 1, so a real transition needs a sequence in %d..%d",
			o.sequence, minSequence, releaseverify.MaxSequence)
	}
	if !channelPattern.MatchString(o.channel) {
		return m, fmt.Errorf("invalid channel %q: want [a-z][a-z0-9_-]{0,63}", o.channel)
	}
	if o.validity <= 0 || o.validity > maxValidity {
		return m, fmt.Errorf("invalid validity %s: want a positive window of at most %s (the verifier caps lifetime at 7 days)",
			o.validity, maxValidity)
	}
	from, err := parseImages(o.fromAPI, o.fromWeb)
	if err != nil {
		return m, fmt.Errorf("from pair: %w", err)
	}
	to, err := parseImages(o.toAPI, o.toWeb)
	if err != nil {
		return m, fmt.Errorf("to pair: %w", err)
	}
	if from == to {
		return m, errors.New("transition must change at least one image: from and to pairs are identical")
	}
	fromAPIRepo, toAPIRepo := repositoryOf(from.API), repositoryOf(to.API)
	fromWebRepo, toWebRepo := repositoryOf(from.Web), repositoryOf(to.Web)
	if fromAPIRepo != toAPIRepo || fromWebRepo != toWebRepo {
		return m, errors.New("from and to must use the same repository per role: the host pins one repository per role and rejects everything else")
	}
	releaseID := o.releaseID
	if releaseID == "" {
		if o.tag == "" {
			return m, errors.New("either --release-id or --tag is required")
		}
		if releaseID, err = deriveReleaseID(o.tag, o.sequence); err != nil {
			return m, err
		}
	}
	if !releaseIDRegex.MatchString(releaseID) {
		return m, fmt.Errorf("invalid release id %q: want [a-z][a-z0-9_-]{0,63}", releaseID)
	}
	if o.out == "" {
		return m, errors.New("--out is required")
	}
	issued := now.Add(-issueBackdate)
	return releaseverify.Manifest{
		Schema:             1,
		Product:            product,
		Channel:            o.channel,
		ReleaseID:          releaseID,
		Sequence:           o.sequence,
		IssuedAt:           issued.Unix(),
		ExpiresAt:          issued.Add(o.validity).Unix(),
		From:               from,
		To:                 to,
		RollbackCompatible: o.rollbackCompatible,
	}, nil
}

// signEnvelope produces exactly the bytes the verifier accepts: payload is
// json.Marshal of the Manifest, the signature is plain Ed25519 over the
// SignatureDomain bytes followed by the payload, and the envelope is the
// compact JSON of Envelope with standard padded base64 and no trailing
// newline.
func signEnvelope(key ed25519.PrivateKey, m releaseverify.Manifest) ([]byte, error) {
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	signature := ed25519.Sign(key, append([]byte(releaseverify.SignatureDomain), payload...))
	envelope, err := json.Marshal(releaseverify.Envelope{
		Payload:   base64.StdEncoding.EncodeToString(payload),
		Signature: base64.StdEncoding.EncodeToString(signature),
	})
	if err != nil {
		return nil, err
	}
	if len(envelope) > releaseverify.MaxEnvelopeSize {
		return nil, fmt.Errorf("envelope exceeds the %d-byte limit the verifier accepts", releaseverify.MaxEnvelopeSize)
	}
	return envelope, nil
}

// selfVerify reconstructs the verifier a host would use (policy pinned to the
// public half of this key, the from repositories, floor 1) and rejects the
// envelope unless the real Verify accepts it for a host whose high-water is
// sequence-1 running exactly the from pair right now.
func selfVerify(key ed25519.PrivateKey, m releaseverify.Manifest, envelope []byte, now time.Time) error {
	apiRepo, webRepo := repositoryOf(m.From.API), repositoryOf(m.From.Web)
	verifier, err := releaseverify.NewVerifier(releaseverify.Policy{
		PublicKey:       base64.StdEncoding.EncodeToString(publicKeyOf(key)),
		Channel:         m.Channel,
		APIRepository:   apiRepo,
		WebRepository:   webRepo,
		MinimumSequence: 1,
	})
	if err != nil {
		return fmt.Errorf("self-verification rejected the reconstructed policy: %w", err)
	}
	verified, err := verifier.Verify(envelope, releaseverify.Context{
		HighWater: m.Sequence - 1,
		Current:   m.From,
		Now:       now,
	})
	if err != nil {
		return fmt.Errorf("self-verification rejected the signed envelope: %w", err)
	}
	if verified.Manifest() != m {
		return errors.New("self-verification accepted a different manifest")
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if verified.PayloadSHA256() != sha256.Sum256(payload) {
		return errors.New("self-verification bound a different payload")
	}
	return nil
}

func parseImages(api, web string) (releaseverify.Images, error) {
	images := releaseverify.Images{API: api, Web: web}
	for _, ref := range []string{api, web} {
		if _, digest, ok := strings.Cut(ref, "@sha256:"); !ok || !digestHex.MatchString(digest) {
			return images, fmt.Errorf("%w: %q", errInvalidImage, ref)
		}
	}
	return images, nil
}

// repositoryOf extracts the pinned repository part of a full reference. The
// verifier's own validImages/validRepository checks are the authority; this
// split only feeds policy reconstruction and operator-readable errors.
func repositoryOf(ref string) string {
	repo, _, _ := strings.Cut(ref, "@sha256:")
	return repo
}

// deriveReleaseID maps a release tag onto the verifier's release ID charset.
// The sequence suffix keeps re-signed releases of the same tag distinct.
func deriveReleaseID(tag string, sequence uint64) (string, error) {
	var b []byte
	for _, r := range strings.ToLower(tag) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b = append(b, byte(r))
		default:
			b = append(b, '-')
		}
	}
	id := fmt.Sprintf("%s-%d", strings.Trim(string(b), "-_"), sequence)
	if id[0] < 'a' || id[0] > 'z' {
		id = "rel-" + id
	}
	if !releaseIDRegex.MatchString(id) {
		return "", fmt.Errorf("cannot derive a valid release id from tag %q: pass --release-id explicitly", tag)
	}
	return id, nil
}

// atomicWriteFile writes the envelope through a same-directory temporary file
// so a partially written envelope can never be observed or published.
func atomicWriteFile(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once renamed: the source path no longer exists
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Chmod(0o644)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	return err
}
