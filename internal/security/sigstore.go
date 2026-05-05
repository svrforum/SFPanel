package security

import "github.com/svrforum/SFPanel/internal/release"

// sigstoreReleaseIdentity is the trusted Sigstore release identity used to
// verify cosign binary downloads during self-bootstrap. SubjectPrefix is the
// GitHub Actions workflow URL prefix matching any tagged release; Issuer is
// the GitHub Actions OIDC token endpoint.
//
// Updating: cosign release pipeline rarely changes its workflow file. If
// they rename release.yaml, this prefix must be updated. There is no
// auto-discovery — that would defeat the whole verification.
var sigstoreReleaseIdentity = release.CosignIdentity{
	SubjectPrefix: "https://github.com/sigstore/cosign/.github/workflows/release.yaml@refs/tags/v",
	Issuer:        "https://token.actions.githubusercontent.com",
}
