package serve

// IsProcessWideTopic exposes processWideTopics to the external serve_test
// package so test helpers can tell broadcast frames from session-scoped ones.
func IsProcessWideTopic(topic string) bool { return processWideTopics[topic] }
