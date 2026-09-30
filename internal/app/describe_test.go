package app_test

import (
	"testing"

	"github.com/Waiivyy/turnback/internal/app"
	"github.com/Waiivyy/turnback/internal/store"
	"github.com/Waiivyy/turnback/internal/testutil"
)

func TestDescribeFromTheDiff(t *testing.T) {
	cases := []struct {
		name  string
		files []store.File
		patch string
		want  string
	}{
		{
			name:  "go: a new function and a changed one",
			files: []store.File{{Status: "M", Path: "http/client.go"}},
			patch: `diff --git a/http/client.go b/http/client.go
--- a/http/client.go
+++ b/http/client.go
@@ -10,3 +10,12 @@ func Get(url string) (*Response, error) {
-	return client.Do(url)
+	return withRetry(func() (*Response, error) { return client.Do(url) })
 }
+
+func withRetry(f func() (*Response, error)) (*Response, error) {
+	return f()
+}
`,
			want: "Add withRetry; update Get",
		},
		{
			name:  "typescript: the demo's sliding window turn",
			files: []store.File{{Status: "M", Path: "src/rateLimit.ts"}},
			patch: `diff --git a/src/rateLimit.ts b/src/rateLimit.ts
--- a/src/rateLimit.ts
+++ b/src/rateLimit.ts
@@ -2,22 +2,18 @@ import type { Request, Response, NextFunction } from "express";
 const LIMIT = 100;
-const hits = new Map<string, { count: number; windowStart: number }>();
+const hits = new Map<string, number[]>();
 
 export function rateLimit(req: Request, res: Response, next: NextFunction) {
-  const windowStart = Math.floor(now / WINDOW_MS) * WINDOW_MS;
+  const recent = (hits.get(key) ?? []).filter((t) => now - t < WINDOW_MS);
@@ -20,5 +16,6 @@ export function rateLimit(req: Request, res: Response, next: NextFunction) {
-  entry.count++;
+  recent.push(now);
`,
			want: "Update hits and rateLimit",
		},
		{
			name:  "top-level code is not credited to the constant above it",
			files: []store.File{{Status: "M", Path: "src/server.ts"}},
			patch: `diff --git a/src/server.ts b/src/server.ts
--- a/src/server.ts
+++ b/src/server.ts
@@ -1,8 +1,9 @@
 import express from "express";
+import { rateLimit } from "./rateLimit";
 import { routes } from "./routes";
 
 const app = express();
 app.use(express.json());
-app.use("/api", routes);
+app.use("/api", rateLimit, routes);
`,
			want: "Update server.ts",
		},
		{
			name:  "python: a new method in a changed class, and a removed function",
			files: []store.File{{Status: "M", Path: "shop/cart.py"}},
			patch: `diff --git a/shop/cart.py b/shop/cart.py
--- a/shop/cart.py
+++ b/shop/cart.py
@@ -1,4 +1,4 @@
-def legacy_total(items):
-    return sum(i.price for i in items)
+import decimal
@@ -8,2 +8,5 @@ class Cart:
+    def discount(self, code):
+        return self.rules.get(code, 0)
`,
			want: "Add discount; update Cart; remove legacy_total",
		},
		{
			name: "new and deleted files are named by file",
			files: []store.File{
				{Status: "A", Path: "src/retry.go"},
				{Status: "D", Path: "src/legacy.go"},
				{Status: "M", Path: "README.md"},
			},
			patch: `diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,1 +1,2 @@
 # app
+Retries failed requests.
`,
			want: "Add retry.go; update README.md; remove legacy.go",
		},
		{
			name:  "a rename keeps its own clause",
			files: []store.File{{Status: "R", OldPath: "docs/old.md", Path: "docs/new.md"}},
			patch: "",
			want:  "Rename docs/old.md to docs/new.md",
		},
		{
			name:  "many names are counted",
			files: []store.File{{Status: "M", Path: "util.go"}},
			patch: `diff --git a/util.go b/util.go
--- a/util.go
+++ b/util.go
@@ -1,0 +1,5 @@
+func a() {}
+func b() {}
+func c() {}
+func d() {}
+func e() {}
`,
			want: "Add a, b, c and 2 more",
		},
		{
			name:  "falls back to files when the sentence gets long",
			files: []store.File{{Status: "M", Path: "long.go"}},
			patch: `diff --git a/long.go b/long.go
--- a/long.go
+++ b/long.go
@@ -1,0 +1,2 @@
+func anExtremelyLongFunctionNameThatNobodyShouldWrite() {}
+func anotherExtremelyLongFunctionNameThatNobodyShouldWrite() {}
`,
			want: "Update long.go",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := app.Describe(c.files, c.patch); got != c.want {
				t.Errorf("Describe = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTurnsWithoutADescriptionAreDescribedFromTheirDiff(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("client.go", "package http\n\nfunc Get(url string) error {\n\treturn nil\n}\n")
	repo.Commit("initial")
	a := openApp(t, repo)
	start(t, a, app.StartOptions{})
	repo.Write("client.go", "package http\n\nfunc Get(url string) error {\n\treturn withRetry(url)\n}\n\nfunc withRetry(url string) error {\n\treturn nil\n}\n")
	res := end(t, a, app.EndOptions{})
	if res.Turn.Description != "Add withRetry; update Get" {
		t.Errorf("description = %q", res.Turn.Description)
	}
}
