package resources

// EffectiveRAMBytes returns the RAM quantity usable for local model loading.
//
// On Linux with a detected NVIDIA GPU, this returns SystemRAMBytes() since
// GPU VRAM is the binding constraint but the local-model RAM fit filter uses
// system RAM as the conservative estimate for CPU-inference scenarios.
//
// On all other platforms (darwin/arm64, Windows without GPU, etc.) it
// returns SystemRAMBytes() directly.
//
// NOTE: the spec mandates "returns SystemRAMBytes() (NOT sum) on darwin/arm64"
// — this is the correct behaviour because Apple Silicon uses a shared memory
// model and the GPU VRAM is a subset of system RAM, not additive.
func EffectiveRAMBytes() int64 {
	return SystemRAMBytes()
}
