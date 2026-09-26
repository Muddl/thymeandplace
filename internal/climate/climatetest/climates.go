// Package climatetest builds synthetic climates for engine and calibration tests.
package climatetest

import (
	"math"

	"thymeandplace/internal/climate"
)

// FromMeans builds normals from explicit monthly means: tmin = mean - diurnal/2,
// tmax = mean + diurnal/2.
func FromMeans(means [12]float64, diurnal float64, precip [12]float64) climate.MonthlyNormals {
	var n climate.MonthlyNormals
	for m := 0; m < 12; m++ {
		n.TminC[m] = means[m] - diurnal/2
		n.TmaxC[m] = means[m] + diurnal/2
		n.PrecipMM[m] = precip[m]
	}
	return n
}

func Flat(meanC, diurnal, precipMM float64) climate.MonthlyNormals {
	var means, precip [12]float64
	for m := range means {
		means[m], precip[m] = meanC, precipMM
	}
	return FromMeans(means, diurnal, precip)
}

// Rotate shifts every monthly value by k months: month m moves to (m+k) mod 12.
func Rotate(n climate.MonthlyNormals, k int) climate.MonthlyNormals {
	var out climate.MonthlyNormals
	for m := 0; m < 12; m++ {
		to := ((m+k)%12 + 12) % 12
		out.TminC[to], out.TmaxC[to], out.PrecipMM[to] = n.TminC[m], n.TmaxC[m], n.PrecipMM[m]
	}
	return out
}

// Temperate is the explicit climate the rule tests reason about by hand.
//
//	month   J  F  M  A  M  J  J  A  S  O  N  D
//	mean    5  8 12 16 20 24 26 25 21 15 10  6
//	tmin    1  4  8 12 16 20 22 21 17 11  6  2   (frost-free: Feb–Nov)
//	precip 60 60 60 60 60 60 60 60 60 40 60 60
func Temperate() climate.MonthlyNormals {
	return FromMeans(
		[12]float64{5, 8, 12, 16, 20, 24, 26, 25, 21, 15, 10, 6}, 8,
		[12]float64{60, 60, 60, 60, 60, 60, 60, 60, 60, 40, 60, 60},
	)
}

// cosine builds a unimodal climate peaking in warmest (0-based month).
func cosine(mean, amplitude float64, warmest int, diurnal float64, precip [12]float64) climate.MonthlyNormals {
	var means [12]float64
	for m := 0; m < 12; m++ {
		means[m] = mean + amplitude*math.Cos(2*math.Pi*float64(m-warmest)/12)
	}
	return FromMeans(means, diurnal, precip)
}

func even(p float64) [12]float64 {
	var a [12]float64
	for i := range a {
		a[i] = p
	}
	return a
}

func Tropical() climate.MonthlyNormals       { return cosine(27, 1, 4, 8, even(200)) }
func TemperateNorth() climate.MonthlyNormals { return cosine(11, 8, 6, 8, even(60)) }
func TemperateSouth() climate.MonthlyNormals { return Rotate(TemperateNorth(), 6) }
func Alpine() climate.MonthlyNormals         { return cosine(3, 9, 6, 10, even(100)) }
func Polar() climate.MonthlyNormals          { return cosine(-12, 14, 6, 6, even(20)) }
func Mediterranean() climate.MonthlyNormals {
	return cosine(17, 7, 7, 10, [12]float64{100, 90, 70, 40, 20, 5, 1, 2, 20, 60, 90, 110})
}

// Continental is a hot-summer, hard-winter climate like New York: the
// coldest month's tmin is -4.0 °C, so tender perennials winter-kill.
func Continental() climate.MonthlyNormals {
	return FromMeans(
		[12]float64{0.5, 2, 6, 12, 17, 22, 25, 24.5, 21, 14.5, 9, 3.5}, 9, even(100),
	)
}

// MaritimeNorth is a mild, cool-summer maritime climate like London.
func MaritimeNorth() climate.MonthlyNormals {
	return FromMeans(
		[12]float64{5, 5.5, 7.5, 10, 13, 16, 18.5, 18, 15.5, 12, 8, 5.5}, 8,
		[12]float64{55, 41, 41, 43, 47, 45, 45, 50, 49, 69, 59, 55},
	)
}

// MaritimeSouth is a mild southern-hemisphere maritime climate like Hobart:
// no month has a mean below 7 °C.
func MaritimeSouth() climate.MonthlyNormals {
	return FromMeans(
		[12]float64{17, 17, 16, 13, 11, 9, 8.5, 9, 11, 13, 14, 16}, 9, even(50),
	)
}

// All returns every named synthetic climate. Each is unimodal, so rotation
// properties hold for them.
func All() map[string]climate.MonthlyNormals {
	return map[string]climate.MonthlyNormals{
		"tropical": Tropical(), "temperate-north": TemperateNorth(), "temperate-south": TemperateSouth(),
		"alpine": Alpine(), "polar": Polar(), "mediterranean": Mediterranean(), "temperate": Temperate(),
		"continental": Continental(), "maritime-north": MaritimeNorth(), "maritime-south": MaritimeSouth(),
	}
}
