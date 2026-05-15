package localruntime

import (
	"context"
	"fmt"
	"net"
	"time"
)

// defaultPorts maps each runtime kind to its well-known localhost port.
var defaultPorts = map[RuntimeKind]int{
	KindOllama:      11434,
	KindLlamaServer: 8080,
	KindLMStudio:    1234,
	KindJan:         1337,
	KindGPT4All:     4891,
}

// defaultBaseURLs maps each runtime kind to its localhost base URL.
var defaultBaseURLs = map[RuntimeKind]string{
	KindOllama:      "http://localhost:11434",
	KindLlamaServer: "http://localhost:8080",
	KindLMStudio:    "http://localhost:1234",
	KindJan:         "http://localhost:1337",
	KindGPT4All:     "http://localhost:4891",
}

// defaultNames maps each runtime kind to its human-readable display name.
var defaultNames = map[RuntimeKind]string{
	KindOllama:      "Ollama",
	KindLlamaServer: "llama-server",
	KindLMStudio:    "LM Studio",
	KindJan:         "Jan",
	KindGPT4All:     "GPT4All",
}

// defaultBinaries maps each runtime kind to the binary name on $PATH.
var defaultBinaries = map[RuntimeKind]string{
	KindOllama:      "ollama",
	KindLlamaServer: "llama-server",
	KindLMStudio:    "lms",
	KindJan:         "jan",
	KindGPT4All:     "gpt4all",
}

// portListening reports whether a TCP connection can be established to
// localhost on the given port within a short dial timeout.
// It is intentionally independent of HTTP to keep the detection fast.
func portListening(ctx context.Context, port int) bool {
	dialCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	d := &net.Dialer{}
	conn, err := d.DialContext(dialCtx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
