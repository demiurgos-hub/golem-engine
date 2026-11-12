package physics3d

import (
	"math"
	"testing"
)

func TestAnchorMotionBoundsContainCurvedOffsetAndLinearOrigin(t *testing.T) {
	from, to := Identity(), Identity()
	to.Position = Vec3{X: 2, Y: 1}
	to.Rotation = Quat{Y: 1}
	local := Vec3{X: 1}
	low, high := AnchorMotionBounds(from, to, local)
	for i := 0; i <= 100; i++ {
		fraction := float32(i) / 100
		angle := float64(fraction) * math.Pi
		actual := Vec3{X: 2*fraction + float32(math.Cos(angle)), Y: fraction, Z: -float32(math.Sin(angle))}
		if actual.X < low.X || actual.Y < low.Y || actual.Z < low.Z || actual.X > high.X || actual.Y > high.Y || actual.Z > high.Z {
			t.Fatalf("arc escaped retained bounds: point=%v low=%v high=%v", actual, low, high)
		}
	}
	if low.Z > -.99 {
		t.Fatal("bounds only included anchor endpoints")
	}
}
