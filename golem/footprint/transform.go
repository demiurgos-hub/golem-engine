package footprint

import (
	"fmt"
	"math"
)

// Transform2D places a 2D footprint with translation, uniform scale, and an
// exact quarter-turn rotation around Z (degrees: 0, 90, 180, or 270, or
// equivalent signed multiples of 90 that normalize to those values).
type Transform2D struct {
	X, Y            float64
	Scale           float64 // must be > 0
	RotationDegrees float64
}

// Transform3D places a 3D footprint with translation, uniform scale, and an
// exact yaw quarter-turn around Y (same degree rules as Transform2D).
type Transform3D struct {
	X, Y, Z    float64
	Scale      float64 // must be > 0
	YawDegrees float64
}

func validateScale(scale float64) error {
	if err := requireFinite("scale", scale); err != nil {
		return fmt.Errorf("footprint: %w", err)
	}
	if scale <= 0 {
		return fmt.Errorf("footprint: scale must be > 0")
	}
	return nil
}

// quarterTurnIndex returns 0..3 for 0/90/180/270 degrees after normalization.
func quarterTurnIndex(degrees float64) (int, error) {
	if err := requireFinite("rotation", degrees); err != nil {
		return 0, fmt.Errorf("footprint: %w", err)
	}
	mod := math.Mod(degrees, 360)
	if mod < 0 {
		mod += 360
	}
	switch mod {
	case 0:
		return 0, nil
	case 90:
		return 1, nil
	case 180:
		return 2, nil
	case 270:
		return 3, nil
	default:
		return 0, fmt.Errorf("footprint: rotation must be an exact quarter turn in degrees (0/90/180/270), got %v", degrees)
	}
}

func transformPoint2D(x, y, scale float64, turn int) (float64, float64) {
	x *= scale
	y *= scale
	switch turn {
	case 1: // 90° CCW around Z: (x,y) -> (-y, x)
		return -y, x
	case 2: // 180°
		return -x, -y
	case 3: // 270° CCW: (x,y) -> (y, -x)
		return y, -x
	default:
		return x, y
	}
}

func transformPoint3D(x, y, z, scale float64, turn int) (float64, float64, float64) {
	x *= scale
	y *= scale
	z *= scale
	// Yaw around Y (Unity Y-up): x' = x cosθ + z sinθ, z' = -x sinθ + z cosθ.
	switch turn {
	case 1: // 90°: x'=z, z'=-x
		return z, y, -x
	case 2: // 180°
		return -x, y, -z
	case 3: // 270°: x'=-z, z'=x
		return -z, y, x
	default:
		return x, y, z
	}
}

func transformAABB2D(w, h, scale float64, turn int) (float64, float64) {
	w *= scale
	h *= scale
	if turn == 1 || turn == 3 {
		return h, w
	}
	return w, h
}

func transformAABB3D(w, h, d, scale float64, turn int) (float64, float64, float64) {
	w *= scale
	h *= scale
	d *= scale
	if turn == 1 || turn == 3 {
		// Swap X/Z full extents on yaw quarter turns.
		return d, h, w
	}
	return w, h, d
}
