// Astronomical position formulas adapted from SunCalc, copyright (c) 2026
// Volodymyr Agafonkin, BSD-2-Clause. See THIRD_PARTY_NOTICES.md.
// Pinned source: https://github.com/mourner/suncalc/blob/21449f34820c3c80a27a78cdc940747ff19ca1e3/index.js
package astronomy

import "math"

const rad = math.Pi / 180

type coordinates struct{ ra, dec, lon, distance float64 }

// Positions use terrestrial time; rotation uses UT. Our supported 2000–2100
// range needs only these three Espenak/Meeus delta-T polynomial segments.
func terrestrial(d float64) float64 {
	y := 2000 + d/365.2425
	t, dt := y-2000, 0.0
	switch {
	case y < 2005:
		dt = 63.86 + t*(.3345+t*(-.060374+t*(.0017275+t*(.000651814+t*.00002373599))))
	case y < 2050:
		dt = 62.92 + t*(.32217+t*.005589)
	default:
		t = (y - 1820) / 100
		dt = -20 + 32*t*t - .5628*(2150-y)
	}
	return d + dt/86400
}

func sun(d float64) coordinates {
	t := d / 36525
	l0 := rad * (280.46646 + t*(36000.76983+t*.0003032))
	m := rad * (357.52911 + t*(35999.05029-t*.0001537))
	sm, cm := math.Sincos(m)
	c := rad * ((1.914602-t*(.004817+t*.000014))*sm + (.019993-.000101*t)*2*sm*cm + .000289*sm*(3-4*sm*sm))
	om := rad * (125.04 - 1934.136*t)
	l := l0 + c - rad*(.00569+.00478*math.Sin(om))
	e := rad*(23.439291-t*(.0130042+t*(.00000016-t*.000000504))) + rad*.00256*math.Cos(om)
	return coordinates{ra: math.Atan2(math.Cos(e)*math.Sin(l), math.Cos(l)), dec: math.Asin(math.Sin(e) * math.Sin(l)), lon: l}
}

func moon(d float64) coordinates {
	t := d / 36525
	lp := 218.3164477 + t*(481267.88123421+t*(-.0015786+t*(1.0/538841-t/65194000)))
	dr := rad * (297.8501921 + t*(445267.1114034+t*(-.0018819+t*(1.0/545868-t/113065000))))
	mr := rad * (357.5291092 + t*(35999.0502909+t*(-.0001536+t/24490000)))
	mpr := rad * (134.9633964 + t*(477198.8675055+t*(.0087414+t*(1.0/69699-t/14712000))))
	fr := rad * (93.2720950 + t*(483202.0175233+t*(-.0036539+t*(-1.0/3526000+t/863310000))))
	a1, a2, a3 := rad*(119.75+131.849*t), rad*(53.09+479264.290*t), rad*(313.45+481266.484*t)
	e := 1 - t*(.002516+t*.0000074)
	factor := func(m float64) float64 {
		switch math.Abs(m) {
		case 1:
			return e
		case 2:
			return e * e
		}
		return 1
	}
	sl, sr, sb := 0.0, 0.0, 0.0
	for _, row := range moonLon {
		s, c := math.Sincos(row[0]*dr + row[1]*mr + row[2]*mpr + row[3]*fr)
		f := factor(row[1])
		sl += row[4] * f * s
		sr += row[5] * f * c
	}
	for _, row := range moonLat {
		sb += row[4] * factor(row[1]) * math.Sin(row[0]*dr+row[1]*mr+row[2]*mpr+row[3]*fr)
	}
	lpr := rad * lp
	sl += 3958*math.Sin(a1) + 1962*math.Sin(lpr-fr) + 318*math.Sin(a2)
	sb += -2235*math.Sin(lpr) + 382*math.Sin(a3) + 175*math.Sin(a1-fr) + 175*math.Sin(a1+fr) + 127*math.Sin(lpr-mpr) - 115*math.Sin(lpr+mpr)
	om, ls, lm := rad*(125.04452-1934.136261*t), rad*(280.4665+36000.7698*t), rad*(218.3165+481267.8813*t)
	dpsi := (-17.20*math.Sin(om) - 1.32*math.Sin(2*ls) - .23*math.Sin(2*lm) + .21*math.Sin(2*om)) / 3600
	deps := (9.20*math.Cos(om) + .57*math.Cos(2*ls) + .10*math.Cos(2*lm) - .09*math.Cos(2*om)) / 3600
	eps := rad * (23.439291 - t*(.0130042+t*(.00000016-t*.000000504)) + deps)
	l, b := rad*(lp+sl/1e6+dpsi), rad*sb/1e6
	return coordinates{ra: math.Atan2(math.Sin(l)*math.Cos(eps)-math.Tan(b)*math.Sin(eps), math.Cos(l)), dec: math.Asin(math.Sin(b)*math.Cos(eps) + math.Cos(b)*math.Sin(eps)*math.Sin(l)), lon: l, distance: 385000.56 + sr/1000}
}

func altitude(d, lat, lon float64, c coordinates) float64 {
	h := rad*(280.46061837+360.98564736629*d+lon) - c.ra
	phi := lat * rad
	return math.Asin(clamp(math.Sin(phi)*math.Sin(c.dec)+math.Cos(phi)*math.Cos(c.dec)*math.Cos(h), -1, 1))
}

// Geometric solar center altitude, degrees. Rise/set threshold includes the
// conventional solar radius and horizon refraction (-0.833 degrees).
func solarHeight(d, lat, lon float64) float64 {
	return altitude(d, lat, lon, sun(terrestrial(d))) / rad
}

func lunarHeight(d, lat, lon float64) float64 {
	c := moon(terrestrial(d))
	h := altitude(d, lat, lon, c)
	h -= math.Asin(6378.14 / c.distance * math.Cos(h)) // topocentric parallax
	// Standard horizon refraction (34 arcmin) plus distance-dependent lunar
	// semidiameter. No weather-dependent refraction or terrain is inferred.
	return h/rad + 34.0/60 + .2725*math.Asin(6378.14/c.distance)/rad
}

func illumination(d float64) (phase, fraction float64) {
	s, m := sun(terrestrial(d)), moon(terrestrial(d))
	phi := math.Acos(clamp(math.Sin(s.dec)*math.Sin(m.dec)+math.Cos(s.dec)*math.Cos(m.dec)*math.Cos(s.ra-m.ra), -1, 1))
	inc := math.Atan2(149598000*math.Sin(phi), m.distance-149598000*math.Cos(phi))
	return math.Mod(math.Mod((m.lon-s.lon)/(2*math.Pi), 1)+1, 1), (1 + math.Cos(inc)) / 2
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
