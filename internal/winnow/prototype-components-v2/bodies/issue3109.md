## Parent

#3104

## What to build

The generator from the reference ticket also writes a TypeScript file for the frontend: the field types, the operators for each type, the enum value lists and the field requirements. Today the query builder keeps these by hand. The query builder imports the generated file, and the hand-written copies are removed. UI labels stay in the translation files. Frontend-only data (field groups in the UI, capability reasons) stays hand-written.

The golden test also compares the generated TypeScript file with the committed one.

## Acceptance criteria

- [ ] The query builder offers the same fields, operators and enum values as before the change.
- [ ] The field types, operators by type, enum lists and requirements are no longer written by hand in the frontend.
- [ ] `make generate` writes the file, and the golden test fails when it is stale.
- [ ] `pnpm` type check, lint and tests pass.

## Blocked by

- #3108

