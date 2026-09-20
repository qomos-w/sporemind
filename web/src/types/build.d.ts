/** Build type: determines which features are available at runtime. */
declare const __BUILD_TYPE__: string;

/** Build flavor: further refinement of the build type (e.g. default, internal, store, cn). */
declare const __BUILD_FLAVOR__: string;

/** Whether the desktop shell was built with the Wails production profile. */
declare const __WAILS_PRODUCTION__: boolean;

/** Build version string injected at build time. */
declare const __BUILD_VERSION__: string;