package main

import (
	"encoding/json"
	"flag"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"io/ioutil"
	"log"
	"math"
	"os"

	"github.com/llgcode/draw2d/draw2dimg"
)

const (
	// Bot attributes
	BotSize      int = 60
	MaxBotSpeed  int = 12
	MaxBotHealth int = 12
	// How much edge margin we should leave
	// when rebuilding the point transformer
	Padding int = BotSize * 2
	// Max horizontal or vertical space that can
	// trigger a point transformer rebuild.
	ShrinkPercent int = 75
	// Renderer settings
	Radians360 float64 = 2 * math.Pi
	// Visual settings
	GridLineSpacing  int = 180
	PowerColorWeight int = 255 / 12
)

var (
	// Images waiting to be converted.
	drawn []*image.RGBA
	// Converted images.
	converted []*image.Paletted
	// Delays between frames.
	delays []int
	// What index we're on for converting
	index chan int
	// When we're done with things
	drawDone    chan bool
	convertDone chan bool
	// Constant colors
	ColorBlack       color.RGBA = color.RGBA{0x00, 0x00, 0x00, 0xff}
	ColorWhite       color.RGBA = color.RGBA{0xff, 0xff, 0xff, 0xff}
	GridColor        color.RGBA = color.RGBA{0x1a, 0x1a, 0x1a, 0xff}
	ColorBlue        color.RGBA = color.RGBA{0x00, 0x00, 0xff, 0xff}
	ColorRed         color.RGBA = color.RGBA{0xff, 0x00, 0x00, 0xff}
	ColorExplosion   color.RGBA = color.RGBA{0xff, 0x00, 0x00, 0xcc}
	ColorTransparent color.RGBA = color.RGBA{0x00, 0x00, 0x00, 0x00}
)

type Point struct {
	X, Y float64
}

type Bot struct {
	PID              int // Player ID
	BID              int // Bot ID
	X, Y             int
	TX, TY           int
	Health           int
	Fired            bool
	HitX, HitY       int
	Scrap            uint
	Shield           bool
	FPow, MPow, SPow int
}

type Tick struct {
	Tick int
	Bots []Bot
}

// Bounds calculates the min and max bounds of the tick.
func (t *Tick) Bounds() image.Rectangle {
	bounds := image.Rectangle{}

	// Initialize to something safe.
	bounds.Min.X = t.Bots[0].X
	bounds.Max.X = t.Bots[0].X
	bounds.Min.Y = t.Bots[0].Y
	bounds.Max.Y = t.Bots[0].Y

	for _, bot := range t.Bots {
		if bot.X > bounds.Max.X {
			bounds.Max.X = bot.X
		}
		if bot.Y > bounds.Max.Y {
			bounds.Max.Y = bot.Y
		}
		if bot.X < bounds.Min.X {
			bounds.Min.X = bot.X
		}
		if bot.Y < bounds.Min.Y {
			bounds.Min.Y = bot.Y
		}
	}
	return bounds
}

type PointTransformer struct {
	Bounds image.Rectangle
	ratio  float64
}

func NewPointTransformer(bounds image.Rectangle, padding, maxDim int) PointTransformer {

	pt := PointTransformer{}
	pt.Bounds = bounds

	// Add padding
	pt.Bounds.Min.X -= padding
	pt.Bounds.Min.Y -= padding
	pt.Bounds.Max.X += padding
	pt.Bounds.Max.Y += padding

	// Figure out which dimension is constrained
	xLength := pt.Bounds.Max.X - pt.Bounds.Min.X
	yLength := pt.Bounds.Max.Y - pt.Bounds.Min.Y

	// X dimension is constrained so X bounds are good as is.
	if xLength > yLength {

		// How much we have to smoosh to fit the image size
		pt.ratio = float64(maxDim) / float64(xLength)

		// Use the extra Y space to expand the bounds
		extra := xLength - yLength
		pt.Bounds.Min.Y -= extra / 2
		pt.Bounds.Max.Y += extra / 2

		// Y dimension is constrained so Y bounds are good as is.
	} else {

		// How much we have to smoosh to fit the image size
		pt.ratio = float64(maxDim) / float64(yLength)

		// Use the extra X space to expand the bounds
		extra := yLength - xLength
		pt.Bounds.Min.X -= extra / 2
		pt.Bounds.Max.X += extra / 2
	}
	return pt
}

func (pt PointTransformer) X(x int) float64 {
	nx := float64(x + -1*pt.Bounds.Min.X)
	return (nx * pt.ratio)
}

func (pt PointTransformer) Y(y int) float64 {
	ny := float64(y + -1*pt.Bounds.Min.Y)
	return (ny * pt.ratio)
}

func (pt PointTransformer) Resize(val int) float64 {
	return float64(val) * pt.ratio
}

func main() {

	var err error

	// Read cli options
	var inFile = flag.String("in", "scrappers.json", "Specify the name of the JSON game file to be rendered.")
	var outFile = flag.String("out", "scrappers.gif", "Specify the name of the GIF to create.")
	var ticksPerSecond = flag.Int("speed", 12, "Specify the GIF speed in ticks per second.")
	var threads = flag.Int("threads", 8, "Specify the number of virtual threads to use while rendering.")
	var imageSize = flag.Int("size", 600, "Specify the dimensions of the square output image.")
	flag.Parse()

	// Input validation
	if *threads < 1 {
		log.Fatalln("Threads must be greater than zero.")
	}
	if *ticksPerSecond > 100 || *ticksPerSecond < 1 {
		log.Fatalln("Speed must be between 1 and 100.")
	}
	if *imageSize < 1 {
		log.Fatalln("Image size must be greater than zero.")
	}

	// Load data file
	dat, err := ioutil.ReadFile(*inFile)
	if err != nil {
		log.Fatalf("Error opening file: %v\n", err)
	}

	// Parse data from file
	var ticks []Tick
	err = json.Unmarshal(dat, &ticks)
	if err != nil {
		log.Fatalf("Failed to parse JSON data: %v\n", err)
	}
	log.Printf("Processing %v ticks.\n", len(ticks))

	drawn = make([]*image.RGBA, len(ticks))
	converted = make([]*image.Paletted, len(ticks))
	delays = make([]int, len(ticks))
	drawDone = make(chan bool)
	convertDone = make(chan bool)
	index = make(chan int)

	// Start rendering
	for n := 1; n <= *threads; n++ {
		go convert()
	}

	// Create the PointTransformer that will do all the
	// coordinate transformations for us.
	tickBounds := ticks[0].Bounds()
	pt := NewPointTransformer(tickBounds, Padding, *imageSize)

	for i, tick := range ticks {

		// If the bounds of this tick fall outside of the bounds
		// of our point transformer, rebuild the point transformer.
		tickBounds = tick.Bounds()
		if tickBounds.Min.Y-BotSize < pt.Bounds.Min.Y ||
			tickBounds.Min.X-BotSize < pt.Bounds.Min.X ||
			tickBounds.Max.Y+BotSize > pt.Bounds.Max.Y ||
			tickBounds.Max.X+BotSize > pt.Bounds.Max.X {
			pt = NewPointTransformer(tickBounds, Padding, *imageSize)
		}

		// If the bounds of this tick are ShrinkPercent or less of the bounds
		// of the point transformer, rebuild point transformer.
		thisYSize := tickBounds.Max.Y - tickBounds.Min.Y
		thisXSize := tickBounds.Max.X - tickBounds.Min.X
		ptYSize := pt.Bounds.Max.Y - pt.Bounds.Min.Y
		ptXSize := pt.Bounds.Max.X - pt.Bounds.Min.X
		if thisYSize*100/ptYSize <= ShrinkPercent && thisXSize*100/ptXSize <= ShrinkPercent {
			pt = NewPointTransformer(tickBounds, Padding, *imageSize)
		}

		// Initialize a new image
		img := image.NewRGBA(image.Rect(0, 0, *imageSize, *imageSize))
		gc := draw2dimg.NewGraphicContext(img)

		// Draw grid lines
		gc.SetStrokeColor(GridColor)
		gc.SetLineWidth(1)
		for x := pt.Bounds.Min.X; x <= pt.Bounds.Max.X; x++ {
			if x%GridLineSpacing == 0 {
				gc.BeginPath()
				gc.MoveTo(pt.X(x), pt.Y(pt.Bounds.Min.Y))
				gc.LineTo(pt.X(x), pt.Y(pt.Bounds.Max.Y))
				gc.Stroke()
			}
		}
		for y := pt.Bounds.Min.Y; y <= pt.Bounds.Max.Y; y++ {
			if y%GridLineSpacing == 0 {
				gc.BeginPath()
				gc.MoveTo(pt.X(pt.Bounds.Min.X), pt.Y(y))
				gc.LineTo(pt.X(pt.Bounds.Max.X), pt.Y(y))
				gc.Stroke()
			}
		}

		sx, sy := -650, -100 // Logo centered at 0,0
		gc.SetLineWidth(pt.Resize(24))
		// S
		gc.BeginPath()
		sAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()
		// C
		sx += 150
		gc.BeginPath()
		cAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()
		// R
		sx += 150
		gc.BeginPath()
		rAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()
		// A
		sx += 150
		gc.BeginPath()
		aAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()
		// P
		sx += 150
		gc.BeginPath()
		pAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()
		// P
		sx += 150
		gc.BeginPath()
		pAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()
		// E
		sx += 150
		gc.BeginPath()
		eAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()
		// R
		sx += 150
		gc.BeginPath()
		rAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()
		// S
		sx += 150
		gc.BeginPath()
		sAt(gc, pt.X(sx), pt.Y(sy), pt.Resize(100))
		gc.Stroke()

		// Draw shots

		gc.SetLineWidth(1)
		gc.SetStrokeColor(ColorRed)
		for _, bot := range tick.Bots {
			if bot.Fired {
				gc.BeginPath()
				gc.MoveTo(pt.X(bot.HitX), pt.Y(bot.HitY))
				gc.LineTo(pt.X(bot.X), pt.Y(bot.Y))
				gc.Stroke()
			}
		}

		// Draw exploded bots

		gc.SetFillColor(ColorExplosion)
		for _, bot := range tick.Bots {
			if bot.Health <= 0 {
				gc.BeginPath()
				circleAt(gc, pt.X(bot.X), pt.Y(bot.Y), pt.Resize(BotSize*2))
				gc.Fill()
			}
		}

		// Draw bot bodies and shields
		for _, bot := range tick.Bots {

			// Skip bots that are dead. We drew an explosion for them
			if bot.Health <= 0 {
				continue
			}

			// Determine body color
			bodyColor := color.RGBA{
				uint8(PowerColorWeight * bot.FPow),
				uint8(PowerColorWeight * bot.MPow),
				uint8(PowerColorWeight * bot.SPow),
				0xff,
			}

			// Draw body

			healthSize := float64(MaxBotHealth-bot.Health) / float64(MaxBotHealth)
			gc.SetStrokeColor(ColorBlack)
			gc.SetFillColor(bodyColor)
			gc.SetLineWidth(1)

			if bot.PID == 1 { // Draw circles

				circleAt(gc, pt.X(bot.X), pt.Y(bot.Y), pt.Resize(BotSize/2))
				gc.FillStroke()

				if healthSize > 0 {
					gc.SetFillColor(ColorBlack)
					gc.SetLineWidth(0)
					circleAt(gc, pt.X(bot.X), pt.Y(bot.Y), pt.Resize(BotSize/2)*healthSize)
					gc.FillStroke()
				}

			} else if bot.PID == 2 { // Draw hexagons

				hexagonAt(gc, pt.X(bot.X), pt.Y(bot.Y), pt.Resize(BotSize/2))
				gc.FillStroke()

				if healthSize > 0 {
					gc.SetFillColor(ColorBlack)
					gc.SetLineWidth(0)
					hexagonAt(gc, pt.X(bot.X), pt.Y(bot.Y), pt.Resize(BotSize/2)*healthSize)
					gc.FillStroke()
				}

			} else {
				log.Fatalf("This program does not support more than two players.\n")
			}

			// Draw shield
			if bot.Shield {
				gc.SetStrokeColor(ColorWhite)
				gc.SetFillColor(ColorTransparent)
				circleAt(gc, pt.X(bot.X), pt.Y(bot.Y), pt.Resize(BotSize/2)*1.1)
				gc.FillStroke()
			}
		}

		drawn[i] = img
		delays[i] = 100 / *ticksPerSecond
		index <- i
		log.Printf("%.1f%%", float64(i*100)/float64(len(ticks)))
	}

	// Let first and last frame linger
	delays[0] = 100 / 100
	delays[len(delays)-1] = 100 / *ticksPerSecond * 10

	// Send complete signals to converting threads
	for n := 1; n <= *threads; n++ {
		drawDone <- true
	}

	// Wait for converting threads to finish
	for n := 1; n <= *threads; n++ {
		<-convertDone
	}

	// Write to disk
	log.Println("Writing image file.")
	f, _ := os.OpenFile(*outFile, os.O_WRONLY|os.O_CREATE, 0600)
	defer f.Close()
	err = gif.EncodeAll(f, &gif.GIF{
		Image: converted,
		Delay: delays,
	})
	if err != nil {
		log.Fatalf("Error writing file: %v\n", err)
	}

	log.Println("Done!")
}

// Convert waits for an index of a RGBA image, then takes that image
// off the staging array and converts it to Paletted image. Or exists
// when the drawDone signal is sent.
func convert() {
	for {
		select {
		case <-drawDone:
			convertDone <- true
			return
		case i := <-index:
			img := drawn[i]
			pal := image.NewPaletted(img.Bounds(), palette.Plan9[:256])
			draw.FloydSteinberg.Draw(pal, img.Bounds(), img, image.ZP)
			converted[i] = pal
			drawn[i] = nil
		}
	}
}

/////////////////////////////
// SHAPE DRAWING FUNCTIONS //
/////////////////////////////

// CircleAt paths a circle to the graphic context
// at the given location and size.
func circleAt(gc *draw2dimg.GraphicContext, x, y, radius float64) {

	// Distance of the control points
	// https://stackoverflow.com/questions/1734745/
	// https://www.wolframalpha.com/input/?i=(4%2F3)*tan(pi%2F(2*2))
	cpDist := radius * 4 / 3

	// hp: high point
	// lp: low point
	// rhcp: right high control point
	// rlcp: right low control point
	// lhcp: left high control point
	// llcp: left low control point
	hp := Point{x, y - radius}
	lp := Point{x, y + radius}
	rhcp := Point{x + cpDist, y - radius}
	rlcp := Point{x + cpDist, y + radius}
	lhcp := Point{x - cpDist, y - radius}
	llcp := Point{x - cpDist, y + radius}

	// Draw circle
	gc.MoveTo(hp.X, hp.Y)
	gc.CubicCurveTo(
		rhcp.X, rhcp.Y,
		rlcp.X, rlcp.Y,
		lp.X, lp.Y,
	)
	gc.CubicCurveTo(
		llcp.X, llcp.Y,
		lhcp.X, lhcp.Y,
		hp.X, hp.Y,
	)
	gc.Close()
}

// HexagonAt paths a hexagon to the graphic context
// at the given location and size.
func hexagonAt(gc *draw2dimg.GraphicContext, x, y, radius float64) {

	sides := 6

	// Calculate where edges meet
	points := make([]Point, 0, sides)
	for s := 1; s <= sides; s++ {
		point := Point{
			x + math.Cos(Radians360*(float64(s)/float64(sides)))*radius,
			y + math.Sin(Radians360*(float64(s)/float64(sides)))*radius,
		}
		points = append(points, point)
	}

	// Draw hexagon
	gc.MoveTo(points[0].X, points[0].Y)
	for s := 1; s < sides; s++ {
		gc.LineTo(points[s].X, points[s].Y)
	}
	gc.Close()
}

//////////////////////////////
// LETTER DRAWING FUNCTIONS //
//////////////////////////////

// SAt draws an S to the graphic context at the given position and size.
func sAt(gc *draw2dimg.GraphicContext, sx, sy, unit float64) {
	gc.MoveTo((sx + unit), (sy))
	gc.LineTo((sx), (sy))
	gc.LineTo((sx), (sy + unit))
	gc.LineTo((sx + unit), (sy + unit))
	gc.LineTo((sx + unit), (sy + unit*2))
	gc.LineTo((sx), (sy + unit*2))
}

// CAt draws a C to the graphic context at the given position and size.
func cAt(gc *draw2dimg.GraphicContext, sx, sy, unit float64) {
	gc.MoveTo((sx + unit), (sy))
	gc.LineTo((sx), (sy))
	gc.LineTo((sx), (sy + unit*2))
	gc.LineTo((sx + unit), (sy + unit*2))
}

// RAt draws an R to the graphic context at the given position and size.
func rAt(gc *draw2dimg.GraphicContext, sx, sy, unit float64) {
	gc.MoveTo((sx), (sy + unit*2))
	gc.LineTo((sx), (sy))
	gc.LineTo((sx + unit), (sy))
	gc.LineTo((sx + unit), (sy + unit))
	gc.LineTo((sx), (sy + unit))
	gc.MoveTo((sx + unit/2), (sy + unit))
	gc.LineTo((sx + unit), (sy + unit*2))
}

// AAt draws an A to the graphic context at the given position and size.
func aAt(gc *draw2dimg.GraphicContext, sx, sy, unit float64) {
	gc.MoveTo((sx), (sy + unit*2))
	gc.LineTo((sx), (sy))
	gc.LineTo((sx + unit), (sy))
	gc.LineTo((sx + unit), (sy + unit*2))
	gc.MoveTo((sx), (sy + unit))
	gc.LineTo((sx + unit), (sy + unit))
}

// PAt draws a P to the graphic context at the given position and size.
func pAt(gc *draw2dimg.GraphicContext, sx, sy, unit float64) {
	gc.MoveTo((sx), (sy + unit*2))
	gc.LineTo((sx), (sy))
	gc.LineTo((sx + unit), (sy))
	gc.LineTo((sx + unit), (sy + unit))
	gc.LineTo((sx), (sy + unit))
}

// EAt draws an E to the graphic context at the given position and size.
func eAt(gc *draw2dimg.GraphicContext, sx, sy, unit float64) {
	gc.MoveTo((sx + unit), (sy))
	gc.LineTo((sx), (sy))
	gc.LineTo((sx), (sy + unit*2))
	gc.LineTo((sx + unit), (sy + unit*2))
	gc.MoveTo((sx), (sy + unit))
	gc.LineTo((sx + unit), (sy + unit))
}
