---
name: scaffold-module
description: Create a new module following Midas Clean Architecture patterns in Go. Generates service implementation, domain entities, interface ports, and wiring.
globs:
  - "internal/services/**"
  - "internal/core/**"
  - "internal/api/**"
---

# Scaffold Module Skill

When invoked with `@scaffold-module {module-name}`, create a complete module structure following the Midas Go Clean Architecture patterns.

## Purpose

Quickly bootstrap new feature services with consistent structure, reducing boilerplate and ensuring architectural compliance with uber/fx DI and strict layer boundaries.

## Prerequisites

Before scaffolding:
1. Check `ARCHITECTURE.md` to verify the module's boundary.
2. Review existing modules under `internal/services/` for consistency.

## Module Structure

```
internal/
├── core/
│   ├── entities/
│   │   └── {module-name}.go         # Domain entity structs
│   └── ports/
│       └── {module-name}.go         # Interfaces (Repository, Service)
├── services/
│   └── {module-name}/
│       ├── service.go               # The Service implementation
│       └── service_test.go          # Basic table-driven tests
└── api/v1/handlers/
    ├── {module-name}.go             # Gin HTTP Handler
    └── {module-name}_test.go        # HTTP Handler tests
```

## File Templates

### Domain Entity (`internal/core/entities/{module-name}.go`)

```go
package entities

// {ModuleName} represents the core domain model
type {ModuleName} struct {
	ID   string
	Name string
}
```

### Interface Ports (`internal/core/ports/{module-name}.go`)

```go
package ports

import (
	"context"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// {ModuleName}Repository defines data storage operations
type {ModuleName}Repository interface {
	GetByID(ctx context.Context, id string) (*entities.{ModuleName}, error)
}

// {ModuleName}Service defines business logic operations
type {ModuleName}Service interface {
	Process(ctx context.Context, id string) (*entities.{ModuleName}, error)
}
```

### Service Implementation (`internal/services/{module-name}/service.go`)

```go
package {module-name}

import (
	"context"
	"fmt"
	
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type ServiceParams struct {
	fx.In
	Repo ports.{ModuleName}Repository
}

type service struct {
	repo ports.{ModuleName}Repository
}

// NewService creates a new instance of {ModuleName}Service.
func NewService(p ServiceParams) ports.{ModuleName}Service {
	return &service{
		repo: p.Repo,
	}
}

func (s *service) Process(ctx context.Context, id string) (*entities.{ModuleName}, error) {
	log := logctx.From(ctx)
	log.Debug("Processing {module-name}", zap.String("id", id))

	result, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get {module-name}: %w", err)
	}

	return result, nil
}
```

### Handler Implementation (`internal/api/v1/handlers/{module-name}.go`)

```go
package handlers

import (
	"net/http"
	
	"github.com/gin-gonic/gin"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"go.uber.org/fx"
)

type {ModuleName}HandlerParams struct {
	fx.In
	Service ports.{ModuleName}Service
}

type {ModuleName}Handler struct {
	service ports.{ModuleName}Service
}

func New{ModuleName}Handler(p {ModuleName}HandlerParams) *{ModuleName}Handler {
	return &{ModuleName}Handler{
		service: p.Service,
	}
}

func (h *{ModuleName}Handler) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/{module-name}")
	{
		group.GET("/:id", h.Get)
	}
}

func (h *{ModuleName}Handler) Get(c *gin.Context) {
	id := c.Param("id")
	
	result, err := h.service.Process(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, result)
}
```

## Post-Scaffold Actions

1. **Register in DI Container**: Update `internal/di/container.go` to provide the new service and handler via `fx.Provide`.
2. **Implement DB Repository**: Add repository implementation mapping to the repository port.
3. **Write Table-Driven Tests**: Flesh out `{module-name}_test.go` and `service_test.go`.
4. **Update Documentation**: Log changes or new features in documentation and APIs in `docs/openapi.yaml`.

## Required Output Format

```
## Module Scaffolded: {module-name}

### Created Files
- [x] internal/core/entities/{module-name}.go
- [x] internal/core/ports/{module-name}.go
- [x] internal/services/{module-name}/service.go
- [x] internal/services/{module-name}/service_test.go
- [x] internal/api/v1/handlers/{module-name}.go

### Next Steps
1. Add fx.Provide for '{module-name}' components in internal/di/container.go
2. Define struct fields in entities.
3. Implement HTTP endpoints and table-driven tests.
```

## Example Usage

```
User: @scaffold-module notifications

AI: [Verifies architecture bounds]
    [Creates Go file structure for core/ports, services, and handlers]
    [Creates template files]
    [Outputs summary]
```
