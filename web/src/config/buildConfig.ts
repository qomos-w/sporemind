export type BuildType = 'release' | 'beta' | 'dev';

/** Build type determined at compile time. Values: 'release', 'beta', 'dev'. */
export const buildType = __BUILD_TYPE__ as BuildType;

/** Build flavor — further refinement (default, internal, store, cn, etc.). */
export const buildFlavor = __BUILD_FLAVOR__ as string;

/**
 * Whether the desktop shell binary was built with the Wails production
 * profile (prod build tags, prod icon/info, `sporemind.exe` name).
 * `make build-desktop` builds a non-production shell; `make release` /
 * `release-desktop` / `beta` / `beta-desktop` build a production one.
 */
export const wailsProduction = __WAILS_PRODUCTION__ as boolean;

/** Build version string. */
export const buildVersion = __BUILD_VERSION__ as string;