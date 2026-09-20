package codegen

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appdef"
)

// testCaptchaAppdef mirrors the captcha demo appdef that used to live in
// plugin-dev-example/ (removed from the repo); it is kept inline here
// because the alias-resolution coverage only needs its struct shapes.
const testCaptchaAppdef = `// app.appdef — Google-style captcha demo (plugin dev audit)
app Captcha {
    id:        "app.captcha"
    name:      "Captcha Demo"
    version:   "0.1.0"
    namespace: "captcha"
    permissions: []

    struct ChallengeRequest {
        optional Difficulty: string
    }
    struct ChallengeResponse {
        ChallengeId:      string
        Kind:             string
        Question:         string
        ImageSvg:         string
        ExpiresInSeconds: int
    }
    struct VerifyRequest {
        ChallengeId: string
        Answer:      string
    }
    struct VerifyResponse {
        Success:    bool
        Score:      double
        ErrorCodes: array<string>
    }
    struct StatsResponse {
        Issued:   int
        Attempts: int
        Passed:   int
    }

    callable challenge {
        request:  ChallengeRequest
        response: ChallengeResponse
        effect:   "read"
        toolName: "captcha-challenge"
    }
    callable verify {
        request:  VerifyRequest
        response: VerifyResponse
        effect:   "mutate"
        toolName: "captcha-verify"
    }
    callable stats {
        response: StatsResponse
        effect:   "read"
        toolName: "captcha-stats"
    }

    entrypoint view main {
        title: "Captcha Demo"
        route: "/"
    }

    event verified {
        payload: VerifyResponse
        permission: "public"
    }

    free_agent {
        allow_create: true
        allow_message: true
        agent_kinds: [coder]
    }
}
`

func TestExampleAppDefAliasResolution(t *testing.T) {
	app, diags, err := appdef.ParseFile(testCaptchaAppdef)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("diags: %v", diags)
	}
	// The captcha appdef has no type aliases; verify the generated schemas
	// resolve array<string> to []string and double to float64 correctly.
	t.Logf("TypeAliases: %+v", app.TypeAliases)
	out, err0 := generateSchemasGo(app)
	if err0 != nil {
		t.Fatal(err0)
	}
	if !strings.Contains(out, "ErrorCodes []string") {
		t.Errorf("ErrorCodes should be []string, got:\n%s", out)
	}
	if !strings.Contains(out, "Score float64") {
		t.Errorf("Score should be float64, got:\n%s", out)
	}
}
