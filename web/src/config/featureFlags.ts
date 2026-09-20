import { buildType } from './buildConfig';
import type { BuildType } from './buildConfig';

/**
 * Feature flags, determined at build time.
 *
 * This is a static mirror of the backend's GetFeatureFlags callable
 * (pkg/buildinfo/info.go). The backend is the authoritative source at
 * runtime; this layer provides build-time dead-code elimination for release
 * builds. The five flag names and their gating rules must stay in sync with
 * the backend's GetFeatureFlags switch.
 */
export const featureFlags = {
  /** Lab mode / experimental features menu. */
  labMode: buildType !== 'release',
  /** Automatic updates (release only). */
  autoUpdate: buildType === 'release',
  /** Crash report submission (release and beta). */
  crashReport: buildType !== 'dev',
  /** Internal-tag badge on UI labels (beta and dev). */
  internalTag: buildType !== 'release',
  /** Allow multiple app instances (dev only). */
  multiInstance: buildType === 'dev',
} as const;

export type FeatureFlagKey = keyof typeof featureFlags;
export { buildType, type BuildType };