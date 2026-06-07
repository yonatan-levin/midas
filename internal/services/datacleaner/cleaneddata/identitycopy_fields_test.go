package cleaneddata

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestIdentityCopy_CoversEveryViewField is the LOW-3 regression pin for
// the identityCopy helper.
//
// Background: identityCopy is a 25-field manual assignment list. Adding a
// new field to FinancialDataView without updating identityCopy produces a
// silent zero-value bug — every view (AsReported, Restated,
// InvestedCapital) carries the seed of identityCopy, so a missed field
// silently zeroes across all three.
//
// This reflection-based test enumerates every field on FinancialDataView,
// builds a *entities.FinancialData with a deliberately distinct non-zero
// value for each named field on the entity that the view also exposes,
// runs identityCopy, and asserts every non-exempt view field is non-zero
// AND matches the entity field value.
//
// Exempt fields are view-only (no matching entity field):
//   - ViewKind: set by accessors, not by identityCopy.
//   - DebtLikeClaims: InvestedCapital-only, populated by overlays.
//   - ExcessCash: InvestedCapital-only (TDB-2 A7), populated by overlays.
//
// When a future field is added to FinancialDataView, the developer must
// EITHER update identityCopy to copy the entity counterpart OR add the
// field to the exempt list (if it is genuinely view-only). The test
// makes the choice explicit.
func TestIdentityCopy_CoversEveryViewField(t *testing.T) {
	exempt := map[string]bool{
		"ViewKind":       true, // set by accessors
		"DebtLikeClaims": true, // InvestedCapital-only, populated by overlays
		"ExcessCash":     true, // TDB-2 A7: InvestedCapital-only, overlay-derived
	}

	// Build a *entities.FinancialData with a deliberately distinct non-
	// zero value for every field that has a matching view counterpart.
	// Float fields use 1e6 * (i+1) so each is uniquely identifiable;
	// strings use the field name; times use a non-zero date.
	raw := &entities.FinancialData{}
	rawV := reflect.ValueOf(raw).Elem()
	nonZeroDate := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)

	viewType := reflect.TypeOf(FinancialDataView{})
	for i := 0; i < viewType.NumField(); i++ {
		name := viewType.Field(i).Name
		if exempt[name] {
			continue
		}
		entityField := rawV.FieldByName(name)
		if !entityField.IsValid() || !entityField.CanSet() {
			// View field has no entity counterpart and is not exempt —
			// either the test forgot to add it to exempt, or the entity
			// field name diverged. Fail loudly.
			t.Fatalf("FinancialDataView.%s has no settable counterpart on entities.FinancialData; "+
				"either rename the view field to match or add %q to the exempt map in this test", name, name)
		}
		switch entityField.Kind() {
		case reflect.Float64:
			// Distinct sentinel — i is the view field index; offset by
			// 1 so no field gets 0.
			entityField.SetFloat(float64(i+1) * 1e6)
		case reflect.String:
			entityField.SetString(name)
		case reflect.Struct:
			// time.Time is the only struct field we copy through; check
			// via type-assertion on the addressable interface.
			if _, ok := entityField.Interface().(time.Time); ok {
				entityField.Set(reflect.ValueOf(nonZeroDate))
			}
		default:
			t.Fatalf("FinancialDataView.%s has unsupported kind %s on the entity — extend this test if a new field type is added", name, entityField.Kind())
		}
	}

	// Run identityCopy.
	out := identityCopy(raw)

	// For every non-exempt view field, assert the output is non-zero
	// AND equals the entity field value.
	outV := reflect.ValueOf(out)
	for i := 0; i < viewType.NumField(); i++ {
		name := viewType.Field(i).Name
		if exempt[name] {
			continue
		}
		entityField := rawV.FieldByName(name)
		if !entityField.IsValid() {
			continue // covered by the earlier Fatalf
		}
		outField := outV.Field(i)
		require.False(t, outField.IsZero(),
			"identityCopy missed field FinancialDataView.%s (entity counterpart is non-zero but view field is zero) — "+
				"add the assignment in cleaneddata/asreported.go::identityCopy, or add %q to the exempt map if the field is view-only",
			name, name)
		require.True(t, reflect.DeepEqual(outField.Interface(), entityField.Interface()),
			"identityCopy produced wrong value for FinancialDataView.%s: got %v, want %v",
			name, outField.Interface(), entityField.Interface())
	}
}
