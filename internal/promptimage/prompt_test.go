// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package promptimage

import "testing"

func TestAdjust(t *testing.T) {
	for _, tc := range []struct{ before, after string }{
		{"Describe these images.", "Describe this image."},
		{"Compare those pictures.", "Compare that picture."},
		{"Both photos show shapes.", "The photo show shapes."},
		{"THESE IMAGES", "THIS IMAGE"},
		{"These IMAGES and those\tPhotos.", "This IMAGE and that\tPhoto."},
		{"图images images图 éimages imagesé", "图images images图 éimages imagesé"},
		{"Describe this image.", "Describe this image."},
		{"Imagine imageries and my_images.", "Imagine imageries and my_images."},
		{"Describe them.", "Describe them."},
	} {
		got, changed := Adjust(tc.before, 1, false)
		if got != tc.after || changed != (tc.before != tc.after) {
			t.Fatalf("%q => %q (%v), want %q", tc.before, got, changed, tc.after)
		}
		for _, count := range []int{0, 2, 3} {
			if got, changed := Adjust(tc.before, count, false); got != tc.before || changed {
				t.Fatal("changed non-single-image input")
			}
		}
		if got, changed := Adjust(tc.before, 1, true); got != tc.before || changed {
			t.Fatal("ignored keep-prompt")
		}
	}
}
