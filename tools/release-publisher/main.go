// Command release-publisher produces releaseverify-compatible signed
// release envelopes for the server-updater. It is the publisher side of the
// v1 signed-release format: it never talks to a registry, fetches an image,
// runs Docker or touches a host. See README.md for the operator workflow.
package main

import (
	"fmt"
	"os"
)

const usageText = `release-publisher: sign server-updater releases (releaseverify v1)

usage:
  release-publisher sign [flags]        sign a release transition
  release-publisher verify-build [flags] verify run-bound build digest provenance
  release-publisher max-sequence FILE... print the highest sequence in the given envelopes
  release-publisher public-key          print the policy public key for the env signing key
  release-publisher keygen              print a new private/public key pair (run locally, once)

sign flags:
  --from-api, --from-web   currently deployed references (repo@sha256:<64 hex>)
  --to-api, --to-web       target references, from the recorded build digests
  --tag                    release tag; derives the release ID when --release-id is absent
  --release-id             globally unique release ID
  --channel                must equal the verifier policy channel
  --sequence               monotonic release sequence (>= 2, above every published envelope)
  --validity               envelope validity window, e.g. 72h (at most 168h)
  --rollback-compatible    mark the transition rollback compatible
  --out                    output envelope path

The Ed25519 private key is read only from the environment variable
PROXIMA_RELEASE_SIGNING_KEY (the GitHub secret RELEASE_SIGNING_KEY). Without
a valid key, or when any validation or self-verification fails, the tool
writes nothing and exits nonzero.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "sign":
		err = runSign(os.Args[2:], os.Stdout)
	case "verify-build":
		err = runVerifyBuild(os.Args[2:], os.Stdout)
	case "max-sequence":
		err = runMaxSequence(os.Args[2:], os.Stdout)
	case "public-key":
		err = runPublicKey(os.Args[2:], os.Stdout)
	case "keygen":
		err = runKeygen(os.Args[2:], os.Stdout)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-publisher:", err)
		os.Exit(1)
	}
}
