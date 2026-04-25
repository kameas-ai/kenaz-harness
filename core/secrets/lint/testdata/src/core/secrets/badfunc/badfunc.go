package badfunc

// A function returning string whose name suggests a credential-bearing
// API must be flagged.

func GetAPIKey() string { // want "secret-bytes: credential-shaped function GetAPIKey returns string"
	return "x"
}

func ReadToken() string { // want "secret-bytes: credential-shaped function ReadToken returns string"
	return "x"
}

// Negative: returns []byte → OK
func ReadSecret() []byte {
	return nil
}

// Negative: name is not credential-shaped → OK
func Region() string {
	return ""
}
