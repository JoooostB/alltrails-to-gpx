package alltrails

import (
	"strings"
	"testing"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name: "valid standard URL",
			url:  "https://www.alltrails.com/trail/netherlands/north-holland/some-trail",
		},
		{
			name: "valid localised URL",
			url:  "https://www.alltrails.com/en-gb/trail/united-kingdom/england/some-trail",
		},
		{
			name: "valid with hyphens in slug",
			url:  "https://www.alltrails.com/trail/us/california/half-dome-via-john-muir-trail",
		},
		{
			name: "valid Dutch locale URL with localised activity type and query string",
			url:  "https://www.alltrails.com/nl-nl/wandelpad/netherlands/south-holland/buytenpark?u=m&sh=x6lehb",
		},
		{
			name: "valid Dutch locale URL with trail activity type",
			url:  "https://www.alltrails.com/nl-nl/trail/netherlands/south-holland/buytenpark?u=m&sh=x6lehb",
		},
		{
			name: "valid URL with query string only",
			url:  "https://www.alltrails.com/trail/us/california/half-dome?u=m",
		},
		{
			name: "valid URL with fragment",
			url:  "https://www.alltrails.com/trail/us/california/half-dome#reviews",
		},
		{
			name:    "missing scheme",
			url:     "www.alltrails.com/trail/us/california/half-dome",
			wantErr: true,
		},
		{
			name:    "http instead of https",
			url:     "http://www.alltrails.com/trail/us/california/half-dome",
			wantErr: true,
		},
		{
			name:    "wrong host",
			url:     "https://alltrails.com/trail/us/california/half-dome",
			wantErr: true,
		},
		{
			name:    "explore page not a trail",
			url:     "https://www.alltrails.com/explore",
			wantErr: true,
		},
		{
			name:    "only two path segments after trail",
			url:     "https://www.alltrails.com/trail/us/california",
			wantErr: true,
		},
		{
			name:    "trailing slash breaks end anchor",
			url:     "https://www.alltrails.com/trail/us/california/half-dome/",
			wantErr: true,
		},
		{
			name:    "empty string",
			url:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
			if tt.wantErr && err != ErrInvalidURL {
				t.Errorf("ValidateURL(%q) = %v, want ErrInvalidURL", tt.url, err)
			}
		})
	}
}

func TestSlugFromURL(t *testing.T) {
	tests := []struct {
		url      string
		wantSlug string
	}{
		{
			url:      "https://www.alltrails.com/trail/us/california/half-dome",
			wantSlug: "half-dome",
		},
		{
			url:      "https://www.alltrails.com/en-gb/trail/united-kingdom/england/some-trail",
			wantSlug: "some-trail",
		},
		{
			url:      "https://www.alltrails.com/nl-nl/wandelpad/netherlands/south-holland/buytenpark?u=m&sh=x6lehb",
			wantSlug: "buytenpark",
		},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := SlugFromURL(tt.url)
			if got != tt.wantSlug {
				t.Errorf("SlugFromURL(%q) = %q, want %q", tt.url, got, tt.wantSlug)
			}
		})
	}
}

func TestExtractTrailID(t *testing.T) {
	const slug = "half-dome"

	tests := []struct {
		name    string
		html    string
		wantID  string
		wantErr error
	}{
		{
			name: "v3/trails URL in link preload (Next.js App Router)",
			html: `<html><head>
				<link rel="preload" as="image" imageSrcSet="https://www.alltrails.com/api/alltrails/v3/trails/10928400/static_map?size=100x100 1x" />
			</head><body></body></html>`,
			wantID: "10928400",
		},
		{
			name:   "trailId in RSC streaming payload",
			html:   `<html><body><script>self.__next_f.push([1,"config":{"trailId":1234567,"mapStyle":"outdoor"}])</script></body></html>`,
			wantID: "1234567",
		},
		{
			name: "legacy: ID in script containing slug",
			html: `<html><body>
				<script>var x = {"slug":"half-dome","id":1234567,"name":"Half Dome"};</script>
			</body></html>`,
			wantID: "1234567",
		},
		{
			name: "legacy: ID with spaces around colon",
			html: `<html><body>
				<script>var x = {"slug":"half-dome","id" : 9876543};</script>
			</body></html>`,
			wantID: "9876543",
		},
		{
			name:    "no trail ID anywhere",
			html:    `<html><body><p>hello</p></body></html>`,
			wantErr: ErrIDNotFound,
		},
		{
			name: "v3/trails ID too short (ignored)",
			html: `<html><body>
				<link rel="preload" imageSrcSet="/api/alltrails/v3/trails/123/static_map" />
			</body></html>`,
			wantErr: ErrIDNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ExtractTrailID(strings.NewReader(tt.html), slug)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("ExtractTrailID() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExtractTrailID() unexpected error: %v", err)
			}
			if id != tt.wantID {
				t.Errorf("ExtractTrailID() = %q, want %q", id, tt.wantID)
			}
		})
	}
}
