package utils

type colors struct {
	c map[string]int
}

var Colors = colors{
	// Palette: https://coolors.co/d4e4bc-d33f49-2176ae-ff8811-ffcfd2
	c: map[string]int{
		"Tea green":      0xd4e4bc,
		"Honolulu Blue":  0x2176ae,
		"Tea rose (red)": 0xffcfd2,
		"Rusty red":      0xd33f49,
		"UT orange":      0xff8811,
	},
}

// Ok returns the color code for success messages
func (c colors) Ok() int {
	return c.c["Tea green"]
}

// Info returns the color code for informational messages
func (c colors) Info() int {
	return c.c["Honolulu Blue"]
}

// Fancy returns the color code for fancy messages
func (c colors) Fancy() int {
	return c.c["Tea rose (red)"]
}

// Error returns the color code for error messages
func (c colors) Error() int {
	return c.c["Rusty red"]
}

// Warning returns the color code for warning messages
func (c colors) Warning() int {
	return c.c["UT orange"]
}
