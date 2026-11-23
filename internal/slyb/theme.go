package slyb

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type Sample struct {
	RGB    [3]int `json:"rgb"`
	Pixels int    `json:"pixels"`
}

func NewThemeCommand() *cobra.Command {
	theme := &cobra.Command{Use: "theme", Short: "What the sidebar looks like on screen"}
	var region string
	sample := &cobra.Command{
		Use:     "sample --region x,y,width,height",
		Short:   "Measure the average color of a screen rectangle in points, as macOS draws it with the window's transparency",
		Example: "slyb theme sample --region=-2558,430,2,400",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(region, ",")
			if len(parts) != 4 {
				return cli.Rejected("--region takes x,y,width,height in points", "")
			}
			numbers := make([]int, 4)
			for i, arg := range parts {
				value, err := strconv.Atoi(arg)
				if err != nil {
					return cli.Rejected("x, y, width and height are whole points", "")
				}
				numbers[i] = value
			}
			home, err := Home()
			if err != nil {
				return err
			}
			path := filepath.Join(home, "cache", "sample.png")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			area := strconv.Itoa(numbers[0]) + "," + strconv.Itoa(numbers[1]) + "," + strconv.Itoa(numbers[2]) + "," + strconv.Itoa(numbers[3])
			if out, err := exec.CommandContext(cmd.Context(), "screencapture", "-x", "-t", "png", "-R", area, path).CombinedOutput(); err != nil {
				return cli.Refused("screencapture failed: "+string(out), "allow Zen to record the screen in System Settings, Privacy and Security")
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			picture, err := png.Decode(file)
			if err != nil {
				return err
			}
			var sum [3]int
			count := 0
			bounds := picture.Bounds()
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					r, g, b, a := picture.At(x, y).RGBA()
					if a == 0 {
						continue
					}
					sum[0] += int(r >> 8)
					sum[1] += int(g >> 8)
					sum[2] += int(b >> 8)
					count++
				}
			}
			if count == 0 {
				return cli.Refused("the sample is empty", "allow Zen to record the screen in System Settings, Privacy and Security")
			}
			return printValue(cmd, Sample{RGB: [3]int{sum[0] / count, sum[1] / count, sum[2] / count}, Pixels: count})
		},
	}
	sample.Flags().StringVar(&region, "region", "", "The screen rectangle in points: x,y,width,height")
	theme.AddCommand(sample)
	return theme
}
