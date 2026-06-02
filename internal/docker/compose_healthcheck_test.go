package docker

import "testing"

func TestHasComposeHealthcheck(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		service string
		want    bool
	}{
		{
			name: "block style present",
			yaml: `services:
  web:
    image: nginx
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost"]
`,
			service: "web",
			want:    true,
		},
		{
			name: "absent",
			yaml: `services:
  web:
    image: nginx
`,
			service: "web",
			want:    false,
		},
		{
			name: "flow style (missed by the old line scanner)",
			yaml: `services:
  web:
    image: nginx
    healthcheck: {test: ["CMD", "true"], interval: 30s}
`,
			service: "web",
			want:    true,
		},
		{
			name: "quoted service name (missed by the old line scanner)",
			yaml: `services:
  "web":
    image: nginx
    healthcheck:
      test: ["CMD", "true"]
`,
			service: "web",
			want:    true,
		},
		{
			name: "anchor + merge key (missed by the old line scanner)",
			yaml: `x-health: &health
  test: ["CMD", "true"]
  interval: 30s
services:
  web:
    image: nginx
    healthcheck:
      <<: *health
`,
			service: "web",
			want:    true,
		},
		{
			name: "disable:true means no effective healthcheck",
			yaml: `services:
  web:
    image: nginx
    healthcheck:
      disable: true
`,
			service: "web",
			want:    false,
		},
		{
			name: "healthcheck on a different service only",
			yaml: `services:
  web:
    image: nginx
  db:
    image: postgres
    healthcheck:
      test: ["CMD", "pg_isready"]
`,
			service: "web",
			want:    false,
		},
		{
			name:    "malformed yaml returns false, never panics",
			yaml:    "services: [this is: not: valid",
			service: "web",
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasComposeHealthcheck(tc.yaml, tc.service); got != tc.want {
				t.Errorf("hasComposeHealthcheck(%q) = %v, want %v", tc.service, got, tc.want)
			}
		})
	}
}
