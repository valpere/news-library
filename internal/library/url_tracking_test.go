package library

import "testing"

func TestCanonicalURLRemovesTrackingParameters(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"linkedin share link from the user",
			"https://www.linkedin.com/posts/andreashorn1_spec-driven-development-is-one-of-the-most-share-7525828798521540608-RgiT/?utm_source=share&utm_medium=member_desktop&rcm=ACoAAAODQxIBAtw12O-IjoHAbP9AlaGQnhdlAm0",
			"https://www.linkedin.com/posts/andreashorn1_spec-driven-development-is-one-of-the-most-share-7525828798521540608-RgiT/"},
		{"any utm_ key", "https://a.example/p?utm_custom=1&id=7", "https://a.example/p?id=7"},
		{"click ids", "https://a.example/p?fbclid=x&gclid=y&msclkid=z&igshid=q&mc_cid=1&mc_eid=2&_hsenc=a&_hsmi=b&yclid=3&dclid=4&twclid=5&gbraid=6&wbraid=7&mkt_tok=8&id=7", "https://a.example/p?id=7"},
		{"linkedin only: trk, lipi, trackingId, rcm", "https://www.linkedin.com/pulse/x/?trk=public_post&lipi=urn&trackingId=t&rcm=r", "https://www.linkedin.com/pulse/x/"},
		{"rcm and trk mean something on other sites", "https://a.example/p?rcm=1&trk=2", "https://a.example/p?rcm=1&trk=2"},
		{"youtube share id", "https://www.youtube.com/watch?v=abc&si=XyZ&feature=share&t=30s", "https://www.youtube.com/watch?v=abc&t=30s"},
		{"si on other sites", "https://a.example/p?si=1", "https://a.example/p?si=1"},
		{"order and encoding of the rest are kept", "https://a.example/p?b=2&a=%D0%B0&utm_source=x", "https://a.example/p?b=2&a=%D0%B0"},
		{"fragment dropped, host lower-cased", "HTTPS://A.Example/p#frag", "https://a.example/p"},
	} {
		got, err := CanonicalURL(c.in)
		if err != nil || got != c.want {
			t.Errorf("%s:\n got %q (%v)\nwant %q", c.name, got, err, c.want)
		}
	}
}

func TestAddURLStoresTheLinkWithoutTrackingParameters(t *testing.T) {
	s := testStore(t)
	raw := "https://www.linkedin.com/posts/x-share-1-AbCd/?utm_source=share&utm_medium=member_desktop&rcm=ACoAAAODQxIB#top"
	id := add(t, s, raw)
	clean := "https://www.linkedin.com/posts/x-share-1-AbCd/"
	if got := scalar[string](t, s, `SELECT original_url FROM items WHERE id=?`, id); got != clean+"#top" {
		t.Fatalf("original_url keeps the address as given minus tracking: %q", got)
	}
	if scalar[int](t, s, `SELECT count(*) FROM source_aliases WHERE url LIKE '%rcm=%' OR url LIKE '%utm_%'`) != 0 {
		t.Fatal("aliases must not keep tracking parameters")
	}
	if again := add(t, s, clean+"?trk=public_post"); again != id {
		t.Fatalf("the same article with other tracking is the same material: %d != %d", again, id)
	}
}
