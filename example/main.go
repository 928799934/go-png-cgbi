package main

import (
	"fmt"
	cgbi "github.com/928799934/go-png-cgbi"
	"image/png"
	"io"
	"io/ioutil"
	"os"
)

func main() {
	f1, err := os.Open("./AppIcon60x60@3x.png")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f1.Close()
	f2, err := ioutil.TempFile("./", "*.png")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f2.Close()
	fmt.Println(ToPNG(f1, f2))

	f3, err := os.Open("./026026565.png")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f3.Close()
	f4, err := ioutil.TempFile("./", "*_cgbi.png")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f4.Close()
	fmt.Println(ToCgBI(f3, f4))
}

func ToCgBI(r io.Reader, w io.Writer) error {
	m, _ := png.Decode(r)
	return cgbi.Encode(w, m)
}

func ToPNG(r io.Reader, w io.Writer) error {
	pic, _ := cgbi.Decode(r)
	return png.Encode(w, pic)
}
