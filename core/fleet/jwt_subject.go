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
