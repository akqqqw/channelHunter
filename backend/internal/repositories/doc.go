// Package repositories contains data access built on sqlc-generated queries.
//
// Rules for this package:
//   - Every workspace-scoped query takes workspace_id and filters by it; tenant
//     isolation is enforced here, not only in the service layer.
//   - Statistics are append-only: INSERT ... ON CONFLICT DO NOTHING, never
//     UPDATE of historical rows.
//   - Money is handled as NUMERIC (pgtype.Numeric / string), never float64.
//
// Hand-written SQL lives in ./queries and is compiled by sqlc (see sqlc.yaml);
// the generated code goes to ./sqlcgen and is committed so builds stay
// hermetic.
package repositories
