package fleet

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// errNoSubject is returned when the access token has no usable `sub` claim.
var errNoSubject = errors.New("fleet: access token has no sub claim")

// SubjectFromAccessToken decodes the `sub` claim from the harness's current
// fleet access token (a Zitadel OIDC JWT) WITHOUT verifying the signature —
// the token was already validated by fleet on every request; here we only
// need the subject identifier locally.
//
// The fleet OTLP receiver (service/telemetry/receiver.go validateResourceAttrs)
// requires the OTel Resource attribute kameas.user.id to equal the JWT `sub`
// (the Zitadel user id), NOT the fleet-internal user UUID returned by /enroll.
// The two are different identity namespaces, so the OTLP pipeline must stamp
// the sub here.
func SubjectFromAccessToken() (string, error) {
	ts, err := LoadTokens()
	if err != nil {
		return "", err
	}
	return subjectFromJWT(ts.AccessToken)
}

// subjectFromJWT extracts the `sub` claim from a JWT's payload segment.
func subjectFromJWT(token string) (string, error) {
	if token == "" {
		return "", errors.New("fleet: empty access token")
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", errors.New("fleet: malformed access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some encoders pad; tolerate standard base64url with padding.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", err
		}
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	if claims.Sub == "" {
		return "", errNoSubject
	}
	return claims.Sub, nil
}

// zitadelResourceOwnerClaim is the access-token claim naming the Zitadel org
// that owns the user. Mirrors kenaz-fleet service/auth.go zitadelOrgIDClaim.
const zitadelResourceOwnerClaim = "urn:zitadel:iam:user:resourceowner:id"

var errNoResourceOwner = errors.New("fleet: access token has no resource-owner org claim")

// ResourceOwnerFromAccessToken returns the Zitadel resource-owner org id from
// the stored access token.
//
// This — not the enroll response's org_id — is what belongs in the
// kameas.org.id resource attribute. Fleet's OTLP receiver
// (validateResourceAttrs) compares kameas.org.id to this exact claim and
// rejects the whole batch with 401 "kameas.org.id mismatch" otherwise. The
// enroll response's org_id is Fleet's INTERNAL org UUID, a different
// namespace: using it meant every export was refused, which is the same trap
// kameas.user.id avoids by using the JWT sub rather than enroll's user_id.
func ResourceOwnerFromAccessToken() (string, error) {
	ts, err := LoadTokens()
	if err != nil {
		return "", err
	}
	return resourceOwnerFromJWT(ts.AccessToken)
}

func resourceOwnerFromJWT(token string) (string, error) {
	if token == "" {
		return "", errors.New("fleet: empty access token")
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", errors.New("fleet: malformed access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", err
		}
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	raw, ok := claims[zitadelResourceOwnerClaim]
	if !ok {
		return "", errNoResourceOwner
	}
	var org string
	if err := json.Unmarshal(raw, &org); err != nil || org == "" {
		return "", errNoResourceOwner
	}
	return org, nil
}


// TokenIdentity is the account identity an access token asserts.
type TokenIdentity struct {
	Subject string // sub
	OrgID   string // Zitadel resource-owner claim
	Issuer  string // iss — the realm
}

// TokenIdentityFromAccessToken decodes the stored access token's identity.
func TokenIdentityFromAccessToken() (TokenIdentity, error) {
	ts, err := LoadTokens()
	if err != nil {
		return TokenIdentity{}, err
	}
	return tokenIdentityFromJWT(ts.AccessToken)
}

func tokenIdentityFromJWT(token string) (TokenIdentity, error) {
	sub, err := subjectFromJWT(token)
	if err != nil {
		return TokenIdentity{}, err
	}
	id := TokenIdentity{Subject: sub}
	id.OrgID, _ = resourceOwnerFromJWT(token)
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, _ = base64.URLEncoding.DecodeString(parts[1])
	}
	var claims struct {
		Iss string `json:"iss"`
	}
	_ = json.Unmarshal(payload, &claims)
	id.Issuer = claims.Iss
	return id, nil
}
