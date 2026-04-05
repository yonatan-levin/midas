---
name: docs-update
description: Update project documentation after code changes. Ensures ARCHITECTURE.md, CONTRACTS.md, TESTING.md stay in sync with code.
---

# Docs Update Skill

When invoked with `@docs-update {scope}`, update relevant documentation to reflect code changes.

## Purpose

Keeps documentation synchronized with code changes. Prevents documentation drift and ensures specs remain accurate.

## Documentation Files

| File | Purpose | When to Update |
|------|---------|----------------|
| `ARCHITECTURE.md` | System architecture, module structure | New modules, structural changes |
| `CONTRACTS.md` | API endpoints, DTOs, schemas | API changes, new endpoints |
| `TESTING.md` | Test strategy, patterns | New test patterns, coverage changes |
| `CLAUDE.md` | Quick reference, commands | Command changes, environment updates |

## Scope Options

- `@docs-update all` - Update all documentation files
- `@docs-update api` - Update for API service changes
- `@docs-update contracts` - Update CONTRACTS.md only
- `@docs-update architecture` - Update ARCHITECTURE.md only
- `@docs-update {service}` - Update service-specific docs

## Automatic Actions

### Step 1: Identify Changes

Analyze recent code changes to determine:
- New modules/services added
- API endpoints changed
- DTOs modified
- Test patterns updated
- Environment variables updated
- Configuration changes
- Database schema changes


### Step 2: Read Current Documentation

Load relevant documentation files to understand current state.

### Step 3: Identify Gaps

Compare code with documentation to find:
- Undocumented features
- Outdated information
- Missing examples
- Incorrect specifications

### Step 4: Generate Updates

Create documentation updates following existing style.

### Step 5: Apply Updates

Update documentation files with new content.

## Documentation Templates

### New Module in ARCHITECTURE.md

```markdown
### {ModuleName} Module

**Location**: `services/{service}/src/modules/{module}/`

**Purpose**: {Brief description of module purpose}

**Components**:
- `{module}.module.ts` - Module definition
- `application/{module}.service.ts` - Business logic
- `presentation/{module}.controller.ts` - REST endpoints
- `infrastructure/{module}.repository.ts` - Data access

**Dependencies**:
- {List of module dependencies}

**Data Flow**:
```
Controller → Service → Repository → Database
```
```

### New Endpoint in CONTRACTS.md

```markdown
### {HTTP Method} /api/{path}

**Description**: {What this endpoint does}

**Authentication**: Required / Optional / None

**Request**:
```typescript
// {RequestDtoName}
{
  field1: string;
  field2: number;
}
```

**Response**:
```typescript
// Success (200)
{
  id: string;
  // ... response fields
}

// Error (4xx/5xx)
{
  statusCode: number;
  message: string;
  error: string;
}
```

**Example**:
```bash
curl -X {METHOD} http://localhost:3000/api/{path} \
  -H "Authorization: Bearer {token}" \
  -H "Content-Type: application/json" \
  -d '{"field1": "value"}'
```
```

### Test Pattern in TESTING.md

```markdown
### {Pattern Name}

**When to use**: {Describe when this pattern applies}

**Example**:
```typescript
describe('{Feature}', () => {
  // Setup
  beforeEach(async () => {
    // ... setup code
  });

  // Test case
  it('should {expected behavior}', async () => {
    // Arrange
    const input = { ... };
    
    // Act
    const result = await service.method(input);
    
    // Assert
    expect(result).toEqual({ ... });
  });
});
```

**Key Points**:
- {Point 1}
- {Point 2}
```

## Required Output Format

```
## Documentation Updated

### Files Updated
| File | Changes |
|------|---------|
| ARCHITECTURE.md | {description of changes} |
| CONTRACTS.md | {description of changes} |
| TESTING.md | {description of changes} |

### Changes Summary

#### ARCHITECTURE.md
- Added: {new sections}
- Updated: {modified sections}
- Removed: {deleted sections}

#### CONTRACTS.md
- New endpoints: {list}
- Updated endpoints: {list}
- New DTOs: {list}

#### TESTING.md
- New patterns: {list}
- Updated coverage requirements: {if changed}

### Verification Checklist
- [ ] All new modules documented
- [ ] All API endpoints have contracts
- [ ] Examples are accurate and runnable
- [ ] No broken internal links
- [ ] Version numbers updated if applicable

### Documentation Health
- Total documented modules: {count}
- Total documented endpoints: {count}
- Documentation coverage: {percentage}%
```

## Documentation Quality Rules

1. **Accuracy**: Code and docs must match
2. **Completeness**: All public APIs documented
3. **Examples**: Include runnable examples
4. **Consistency**: Follow existing style
5. **Currency**: Remove deprecated info

## Composability

This skill works with:
- After `@scaffold-module` - document new module
- After implementation complete - update contracts
- Before `@review-prep` - ensure docs are current

## Example Usage

```
User: @docs-update api

AI: [Reads current ARCHITECTURE.md, CONTRACTS.md]
    [Analyzes services/api/ for changes]
    [Identifies new endpoints, modules]
    [Updates documentation files]
    [Outputs summary of changes]
```

## Auto-Detection

When invoked without scope, attempt to detect:
1. Which services were recently modified
2. What types of changes were made
3. Which documentation files need updates

```
User: @docs-update

AI: [Detects recent changes to services/api/]
    [Identifies new rate-limiting module]
    [Updates ARCHITECTURE.md with module info]
    [Updates CONTRACTS.md with new endpoints]
    [Outputs comprehensive update summary]
```
