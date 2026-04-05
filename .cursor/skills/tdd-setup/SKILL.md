---
name: tdd-setup
description: Set up Test-Driven Development environment for a feature. Creates test file structure with failing tests before implementation.
---

# TDD Setup Skill

When invoked with `@tdd-setup {feature-name} {service}`, create test files and structure for Test-Driven Development workflow.

## Purpose

Ensures TDD methodology is followed by creating comprehensive test structure before implementation. Tests should fail initially and pass after implementation.

## TDD Workflow

```
┌─────────────────────────────────────────────────────────────┐
│                    TDD Cycle (Red-Green-Refactor)           │
├─────────────────────────────────────────────────────────────┤
│  1. @tdd-setup → Create failing tests (RED)                 │
│  2. Implement minimal code to pass (GREEN)                  │
│  3. Refactor while keeping tests green (REFACTOR)           │
│  4. Repeat for next feature                                 │
└─────────────────────────────────────────────────────────────┘
```

## Automatic Actions

### Step 1: Load Testing Requirements

Read `services/{service}/TESTING.md` to understand:
- Coverage requirements (target: 90%+)
- Test patterns used
- E2E vs integration vs unit balance

### Step 2: Analyze Feature Scope

Identify:
- Happy path scenarios
- Error cases
- Edge cases
- Integration points

### Step 3: Create Test File Structure

**For Backend Services:**

```
services/{service}/test/
├── {feature}.e2e-spec.ts          # E2E tests (primary)
└── {feature}.integration-spec.ts   # Integration tests (if needed)

services/{service}/src/modules/{module}/__tests__/
└── {feature}.service.spec.ts       # Unit tests (sanity only)
```

**For Frontend:**

```
services/frontend/src/features/{feature}/
├── __tests__/
│   ├── {Component}.test.tsx        # Component tests
│   └── {hook}.test.ts              # Hook tests
└── {feature}.e2e.spec.ts           # E2E with Playwright
```

## Test Templates

### E2E Test Template (Backend)

```typescript
import { Test, TestingModule } from '@nestjs/testing';
import { INestApplication, ValidationPipe } from '@nestjs/common';
import * as request from 'supertest';
import { AppModule } from '../src/app.module';

/**
 * E2E Tests for {Feature}
 * 
 * These tests verify the complete flow from HTTP request to response,
 * including database operations and external service calls.
 */
describe('{Feature} E2E', () => {
  let app: INestApplication;

  beforeAll(async () => {
    const moduleFixture: TestingModule = await Test.createTestingModule({
      imports: [AppModule],
    }).compile();

    app = moduleFixture.createNestApplication();
    app.useGlobalPipes(new ValidationPipe());
    await app.init();
  });

  afterAll(async () => {
    await app.close();
  });

  // ============================================
  // HAPPY PATH TESTS
  // ============================================
  
  describe('Happy Path', () => {
    it.todo('should {expected behavior 1}');
    it.todo('should {expected behavior 2}');
    it.todo('should {expected behavior 3}');
  });

  // ============================================
  // ERROR CASES
  // ============================================
  
  describe('Error Cases', () => {
    it.todo('should return 400 when {invalid input}');
    it.todo('should return 401 when {unauthorized}');
    it.todo('should return 404 when {not found}');
    it.todo('should return 500 when {server error}');
  });

  // ============================================
  // EDGE CASES
  // ============================================
  
  describe('Edge Cases', () => {
    it.todo('should handle {edge case 1}');
    it.todo('should handle {edge case 2}');
  });

  // ============================================
  // INTEGRATION POINTS
  // ============================================
  
  describe('Integration', () => {
    it.todo('should integrate with {external service}');
    it.todo('should handle {service} unavailability');
  });
});
```

### Frontend Component Test Template

```typescript
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { vi } from 'vitest';
import { {Component} } from '../{Component}';

/**
 * Tests for {Component}
 */
describe('{Component}', () => {
  // ============================================
  // RENDERING TESTS
  // ============================================
  
  describe('Rendering', () => {
    it.todo('should render initial state');
    it.todo('should render loading state');
    it.todo('should render error state');
    it.todo('should render success state');
  });

  // ============================================
  // INTERACTION TESTS
  // ============================================
  
  describe('Interactions', () => {
    it.todo('should handle {user action 1}');
    it.todo('should handle {user action 2}');
  });

  // ============================================
  // ACCESSIBILITY
  // ============================================
  
  describe('Accessibility', () => {
    it.todo('should be keyboard navigable');
    it.todo('should have proper ARIA labels');
  });
});
```

## Test Case Generation

Use `sequential-thinking` to identify test cases:

1. **Happy Path**: Normal successful operations
2. **Validation Errors**: Invalid inputs, missing fields
3. **Auth Errors**: Unauthorized, forbidden
4. **Not Found**: Missing resources
5. **Concurrency**: Race conditions, duplicate requests
6. **Timeouts**: External service delays
7. **Edge Cases**: Empty lists, max values, special characters

## Required Output Format

```
## TDD Setup Complete: {feature} in {service}

### Test Files Created
- [x] test/{feature}.e2e-spec.ts
- [x] src/modules/{module}/__tests__/{feature}.service.spec.ts

### Test Cases Identified

#### Happy Path (X tests)
- [ ] {test case 1}
- [ ] {test case 2}

#### Error Cases (X tests)
- [ ] {test case 1}
- [ ] {test case 2}

#### Edge Cases (X tests)
- [ ] {test case 1}

### Run Tests
```bash
# Run tests (should fail - RED phase)
npm run -w services/{service} test:e2e -- --grep "{feature}"

# Watch mode
npm run -w services/{service} test:e2e -- --watch
```

### TDD Next Steps
1. Run tests → Verify they FAIL (Red phase)
2. Implement minimal code to pass
3. Run tests → Verify they PASS (Green phase)
4. Refactor while keeping tests green
5. Repeat for remaining test cases

### Coverage Target
- Required: ≥ 90%
- Current: 0% (tests created but not passing)
```

## Composability

This skill chains well with:
- `@preflight` - run first for context
- `@load-context {path}` - understand service, package, or infrastructure patterns
- `@scaffold-module {module} {service}` - create module structure first

## Example Usage

```
User: @tdd-setup rate-limiting api

AI: [Reads services/api/TESTING.md]
    [Uses sequential-thinking to identify test cases]
    [Creates E2E test file with TODO tests]
    [Outputs TDD setup summary]
```
