// Vitest 5 changed `Assertion` from a single type parameter to
// `Assertion<R, T>`, which breaks the type augmentation shipped in
// `@testing-library/jest-dom/vitest` (still targeting `Assertion<T>`).
// This restores the jest-dom matcher types by augmenting `Matchers<R, T>`
// instead, per the documented Vitest 5 extension point.
// See: https://github.com/testing-library/jest-dom/issues/738
import "vitest";
import type { ExpectStatic } from "vitest";
import type { TestingLibraryMatchers } from "@testing-library/jest-dom/matchers";

type AsymmetricMatcher = ReturnType<ExpectStatic["stringContaining"]>;

declare module "vitest" {
  interface Matchers<R, T> extends TestingLibraryMatchers<AsymmetricMatcher, R> {}
  interface AsymmetricMatchersContaining extends TestingLibraryMatchers<AsymmetricMatcher, any> {}
}
