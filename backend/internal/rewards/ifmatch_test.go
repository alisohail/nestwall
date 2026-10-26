package rewards

import (
	"net/http"
	"testing"
)

func TestParseIfMatch(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		want    int
		wantErr bool
	}{
		{name: "absent", header: "", want: -1},
		{name: "version zero", header: `"0"`, want: 0},
		{name: "version three", header: `"3"`, want: 3},
		{name: "weak version zero", header: `W/"0"`, want: 0},
		{name: "unquoted", header: "2", want: 2},
		{name: "malformed", header: "abc", wantErr: true},
		{name: "negative", header: `"-1"`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodPut, "/", nil)
			if tc.header != "" {
				r.Header.Set("If-Match", tc.header)
			}
			got, err := parseIfMatch(r)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
