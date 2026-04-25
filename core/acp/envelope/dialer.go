package envelope

// This file is the canonical home of the Dialer / Listener / Conn
// interfaces consumed by transport sub-packages. Splitting them out
// from envelope.go keeps the SDK-touching surface (eventually) and
// the transport seam visually distinct.
//
// The interfaces are defined in envelope.go to keep godoc grouping
// tight; this file exists so transport packages have an obvious
// import target when someone is reading the layout for the first
// time.
