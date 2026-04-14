# Y2: Replace init() with TestMain() in server_test.go

**Status:** OPEN
**Severity:** YELLOW (test quality)
**Found by:** REVIEWER (2026-04-14)
**Location:** `internal/api/server_test.go:107`

## Description

The test file uses a package-level `init()` function to set `gin.SetMode(gin.TestMode)`. This runs even when tests in this package are not being executed. The idiomatic Go approach is `TestMain(m *testing.M)`, which only runs when tests in the package are invoked.

## Recommended Fix

```go
func TestMain(m *testing.M) {
    gin.SetMode(gin.TestMode)
    os.Exit(m.Run())
}
```

Remove the `init()` function.

## Risk if Not Fixed

Minimal. Causes a harmless global side effect when other packages are tested.
