package fleet

import (
	"errors"
	"math/big"
)

// checkPinnableEd25519Key rejects 32-byte encodings that must never be a
// trust anchor:
//
//   - non-canonical encodings (y ≥ p, or x = 0 with the sign bit set),
//   - encodings that do not decode to a point on edwards25519,
//   - small-order points (order 1, 2, 4 or 8 — the identity, the all-zero
//     encoding and their kin).
//
// A small-order public key makes signatures forgeable without any private
// key: with A = identity, the forged signature (R = identity, S = 0)
// satisfies [S]B = R + [k]A for every message, and Go's ed25519.Verify
// accepts it. Pinning such a key would let anyone sign bundles, so the
// whole pin set is rejected at parse (bundle-key-rotation review F2).
//
// The stdlib exposes no point-validation API (crypto/internal/edwards25519
// is not importable) and the repo carries no edwards25519 module, so this
// is a direct RFC 8032 §5.1.3 decode plus three affine doublings ([8]P) in
// math/big. It runs only over the handful of build-time pins.
func checkPinnableEd25519Key(enc []byte) error {
	if len(enc) != 32 {
		return errors.New("is not 32 bytes")
	}
	x, y, ok := decodeEd25519Point(enc)
	if !ok {
		return errors.New("is not a canonical encoding of an edwards25519 point")
	}
	// [8]P: three doublings. P has small order iff [8]P is the identity.
	for i := 0; i < 3; i++ {
		x, y = edAdd(x, y, x, y)
	}
	if x.Sign() == 0 && y.Cmp(big.NewInt(1)) == 0 {
		return errors.New("is a small-order (degenerate) point — signatures under it are forgeable")
	}
	return nil
}

var (
	edP = func() *big.Int {
		p := new(big.Int).Lsh(big.NewInt(1), 255)
		return p.Sub(p, big.NewInt(19))
	}()
	// edD = -121665 / 121666 mod p.
	edD = func() *big.Int {
		num := new(big.Int).Neg(big.NewInt(121665))
		den := new(big.Int).ModInverse(big.NewInt(121666), edP)
		num.Mul(num, den)
		return num.Mod(num, edP)
	}()
	// edSqrtM1 = 2^((p-1)/4) mod p, a square root of -1.
	edSqrtM1 = func() *big.Int {
		e := new(big.Int).Sub(edP, big.NewInt(1))
		e.Rsh(e, 2)
		return new(big.Int).Exp(big.NewInt(2), e, edP)
	}()
)

// decodeEd25519Point decodes per RFC 8032 §5.1.3, rejecting non-canonical
// encodings and non-points.
func decodeEd25519Point(enc []byte) (x, y *big.Int, ok bool) {
	le := make([]byte, 32)
	copy(le, enc)
	sign := uint(le[31] >> 7)
	le[31] &= 0x7f
	be := make([]byte, 32)
	for i := range le {
		be[31-i] = le[i]
	}
	y = new(big.Int).SetBytes(be)
	if y.Cmp(edP) >= 0 {
		return nil, nil, false // non-canonical y
	}
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, edP)
	u := new(big.Int).Sub(y2, big.NewInt(1))
	u.Mod(u, edP)
	v := new(big.Int).Mul(edD, y2)
	v.Add(v, big.NewInt(1))
	v.Mod(v, edP)
	vInv := new(big.Int).ModInverse(v, edP)
	if vInv == nil {
		return nil, nil, false
	}
	x2 := new(big.Int).Mul(u, vInv)
	x2.Mod(x2, edP)

	// Candidate root x = x2^((p+3)/8).
	e := new(big.Int).Add(edP, big.NewInt(3))
	e.Rsh(e, 3)
	x = new(big.Int).Exp(x2, e, edP)
	if !sqEquals(x, x2) {
		x.Mul(x, edSqrtM1)
		x.Mod(x, edP)
		if !sqEquals(x, x2) {
			return nil, nil, false // not on the curve
		}
	}
	if x.Sign() == 0 && sign == 1 {
		return nil, nil, false // non-canonical: -0
	}
	if x.Bit(0) != sign {
		x.Sub(edP, x)
	}
	return x, y, true
}

func sqEquals(x, want *big.Int) bool {
	s := new(big.Int).Mul(x, x)
	s.Mod(s, edP)
	return s.Cmp(want) == 0
}

// edAdd is the complete twisted-Edwards addition law for a = -1:
//
//	x3 = (x1·y2 + y1·x2) / (1 + d·x1·x2·y1·y2)
//	y3 = (y1·y2 + x1·x2) / (1 − d·x1·x2·y1·y2)
//
// Denominators never vanish on edwards25519 (d is a non-square).
func edAdd(x1, y1, x2, y2 *big.Int) (*big.Int, *big.Int) {
	mod := func(z *big.Int) *big.Int { return z.Mod(z, edP) }
	t := mod(new(big.Int).Mul(edD, mod(new(big.Int).Mul(mod(new(big.Int).Mul(x1, x2)), mod(new(big.Int).Mul(y1, y2))))))
	xn := mod(new(big.Int).Add(new(big.Int).Mul(x1, y2), new(big.Int).Mul(y1, x2)))
	yn := mod(new(big.Int).Add(new(big.Int).Mul(y1, y2), new(big.Int).Mul(x1, x2)))
	xd := mod(new(big.Int).Add(big.NewInt(1), t))
	yd := mod(new(big.Int).Sub(big.NewInt(1), t))
	x3 := mod(new(big.Int).Mul(xn, new(big.Int).ModInverse(xd, edP)))
	y3 := mod(new(big.Int).Mul(yn, new(big.Int).ModInverse(yd, edP)))
	return x3, y3
}
