package services

import "strings"

// Codes without an ISO 4217 minor unit (XAU, XDR, XTS, ...) are deliberately
// absent: they are rejected rather than rounded to a guessed precision.
var minorUnitsByCurrency = func() map[string]int {
	m := map[string]int{}
	add := func(units int, codes string) {
		for _, code := range strings.Fields(codes) {
			m[code] = units
		}
	}
	add(2, `AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BMD BND BOB BRL BSD BTN BWP BYN BZD CAD CDF CHF CNY COP CRC CUP CVE CZK DKK DOP DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GTQ GYD HKD HNL HTG HUF IDR ILS INR IRR JMD KES KGS KHR KPW KYD KZT LAK LBP LKR LRD LSL MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MYR MZN NAD NGN NIO NOK NPR NZD PAB PEN PGK PHP PKR PLN QAR RON RSD RUB SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP STN SYP SZL THB TJS TMT TOP TRY TTD TWD TZS UAH USD UYU UZS VES WST XCD XCG YER ZAR ZMW ZWG`)
	add(0, `BIF CLP DJF GNF ISK JPY KMF KRW PYG RWF UGX VND VUV XAF XOF XPF`)
	add(3, `BHD IQD JOD KWD LYD OMR TND`)
	return m
}()

func MinorUnits(currency string) (int, bool) {
	units, ok := minorUnitsByCurrency[currency]
	return units, ok
}
