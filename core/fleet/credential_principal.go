package fleet

import "github.com/kameas-ai/kenaz-harness/core/secrets"

// CredentialPrincipal returns the hashed (realm, user, org) the stored access
// token asserts, which scopes Fleet-delivered provider keys in the keychain
// (secrets.FleetCredentialLocator: kenaz-harness/fleet/<env>/<realm>/<sub>/…;
// provider-credentials-client-01PCCLI01 WP04). It returns
// secrets.ErrNoFleetPrincipal when Fleet is disabled or no usable token is
// stored, so every Fleet key fails closed while signed out.
func CredentialPrincipal() (secrets.FleetPrincipal, error) {
	if Disabled() {
		return secrets.FleetPrincipal{}, secrets.ErrNoFleetPrincipal
	}
	id, err := TokenIdentityFromAccessToken()
	if err != nil {
		return secrets.FleetPrincipal{}, secrets.ErrNoFleetPrincipal
	}
	return secrets.NewFleetPrincipal(id.Issuer, id.Subject, id.OrgID)
}
