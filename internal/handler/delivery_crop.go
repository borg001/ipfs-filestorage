package handler

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// cropFallbackVariant is the size a framed avatar is served at when the
// request names none of the configured sizes - the original, say.
const cropFallbackVariant = "fit_1024"

// deliveryCrop is the part of a picture a link shows: an avatar framed in the
// gallery (theGHub1/api#591), as fractions of the picture's width and height
// from its top left corner.
type deliveryCrop struct {
	X, Y, W, H float64
}

// parseDeliveryCrop reads a frame written "x,y,w,h". Anything that is not
// four fractions naming a part of the picture is no frame.
func parseDeliveryCrop(value string) (*deliveryCrop, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false
	}
	parts := strings.Split(value, ",")
	if len(parts) != 4 {
		return nil, false
	}
	numbers := make([]float64, 4)
	for index, part := range parts {
		number, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || math.IsNaN(number) || number < 0 || number > 1 {
			return nil, false
		}
		numbers[index] = number
	}
	crop := &deliveryCrop{X: numbers[0], Y: numbers[1], W: numbers[2], H: numbers[3]}
	if crop.W < 0.02 || crop.H < 0.02 || crop.X+crop.W > 1.0001 || crop.Y+crop.H > 1.0001 {
		return nil, false
	}
	return crop, true
}

// key is the frame as it names a cached rendition.
func (c *deliveryCrop) key() string {
	return fmt.Sprintf("%.4f,%.4f,%.4f,%.4f", c.X, c.Y, c.W, c.H)
}
