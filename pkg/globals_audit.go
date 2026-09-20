// Package globals_audit is a documentation-only file that inventories every
// package-level var declaration across the sporemind Go codebase (excluding
// tests, generated files, and the plugin SDK which is ABI-bound by design).
//
// This file intentionally has NO code — it exists so `go doc` and IDE search
// surface the audit. Each var is classified into one of:
//
//   A. Error sentinel   — var ErrXxx = errors.New(...) / fmt.Errorf(...)
//   B. Lookup table     — var xxx = map[...]...{...} / []T{...}, never mutated
//   C. Compiled regexp  — var xxx = regexp.MustCompile(...)
//   D. Embedded FS      — //go:embed + var xxx embed.FS
//   E. Resource key     — var XxxKey = resource.NewKey[...]
//   F. Mutable singleton— read AND written at runtime (violates "Actor 优先")
//   G. Function seam    — var xxxFunc = defaultImpl (test injection point)
//   H. Compliance assert— var _ = ... (interface satisfaction check)
//   I. Other            — platform/ABI/edge-case
//
// ── pkg/actor/agent ──────────────────────────────────────────────────────
//
//   agentStore          F  persist.Persist singleton (agent.go:71)
//   nativeToolRegistry  F  nativetools.Registry singleton (agent.go:1682)
//   explorerNamePool    B  []string name pool (agent_explore.go:26)
//   generalNamePool     B  []string name pool (agent_explore.go:770)
//   builtinSkillIDSet   B  sync.OnceValue map (agent_skill.go:22)
//   summarizeViaPlan    G  test seam (compaction.go:204)
//   probeActualTokensFn G  test seam (compaction.go:361)
//   fileNameSanitizer   B  strings.NewReplacer (memory/monocard.go:136)
//   errPaused           A  errors.New (turn_engine_dispatch.go:37)
//   dispatchRetryBackoffs B []time.Duration (turn_engine_dispatch.go:59)
//   fileMutatingCallables B map[string]bool (turn_engine_execute.go:876)
//   shellFilesystemIntercepts B map (turn_engine_execute.go:2872)
//   externalBinaryAllowlist  B map[string]bool (turn_engine_execute.go:2884)
//   vfsBuiltins         B  map[string]bool (turn_engine_execute.go:2901)
//
// ── pkg/actor/aiaggregator ───────────────────────────────────────────────
//
//   errUnitsTokenPlanExhausted A errors.New (actor.go:678)
//
// ── pkg/actor/aimanager ──────────────────────────────────────────────────
//
//   supportedProtocols  B  map[string]bool (aimanager.go:1131)
//
// ── pkg/actor/appmanager ─────────────────────────────────────────────────
//
//   gateOrder           B  []GateName (gates.go:24)
//   sporeAppInvokeRespDesc B sporesch.TypeDesc (lifecycle.go:374)
//   hostBridgeDispatchServices B []string (pluginhost/pluginhost.go:622)
//
// ── pkg/actor/browserinstance ────────────────────────────────────────────
//
//   (var block)         B  browser config maps (browserinstance.go:41)
//
// ── pkg/actor/computeruse ────────────────────────────────────────────────
//
//   ensurePaddleModels  G  test seam (computeruse.go:1174)
//   gatedCallables      B  map[string]gatedCallable (policy.go:35)
//   InteractActions     B  []string (internal/capture/capabilities.go:21)
//   NewWindowsCapturer  G  platform seam (internal/capture/capture.go:125)
//   NewLinuxCapturer    G  platform seam (internal/capture/capture.go:129)
//   NewDarwinCapturer   G  platform seam (internal/capture/capture.go:133)
//   (paddle var blocks) B  paddle model config (internal/capture/ocr/paddle.go:19,84)
//   ensurePaddleModelsFn G test seam (internal/capture/ocr/paddle.go:95)
//   ErrNotAvailable     A  errors.New (internal/capture/ocr/tesseract.go:21)
//   (tesseract var block) B tesseract config (internal/capture/ocr/tesseract.go:38)
//   errFrameTooLarge    A  errors.New (internal/capture/winx/uia_protocol.go:63)
//   uiaHelperMode       I  atomic.Bool (internal/capture/winx/uia_helper_main.go:23)
//   (winx var blocks)   I  COM vtables / win32 defs (winx/*.go)
//
// ── pkg/actor/filesystem ─────────────────────────────────────────────────
//
//   posixCharClassRe    C  regexp (filesystem.go:42)
//   posixToGoClass      B  map[string]string (filesystem.go:45)
//   defaultSkipDirs     B  map[string]bool (filesystem.go:599)
//
// ── pkg/actor/interfacemanager ───────────────────────────────────────────
//
//   validViewModes      B  map (interfacemanager.go:33)
//   validSettingCategories B map (interfacemanager.go:44)
//   validInteractionKinds  B map (interfacemanager.go:61)
//
// ── pkg/actor/project ────────────────────────────────────────────────────
//
//   sporeScalarTypes    B  map (card_contract.go:30)
//   (card_contract var block) B map (card_contract.go:51)
//   canonicalCardTypes  B  map (card_metadata.go:75)
//   canonicalTaskStatuses B []string (card_mounting.go:22)
//   defaultTaskStatusAliases B map (card_mounting.go:28)
//   taskStatusNode      B  map (card_mounting.go:77)
//   statusBuiltinVisuals B map (card_mounting.go:89)
//   builtinMountSpecs   B  []mountSpec (card_mounting.go:103)
//   builtinMountVisuals B  map (card_mounting.go:269)
//   wellKnownCardParents B map (card_mounting.go:396)
//   cardTypeRepairers   B  map (card_repair.go:16)
//   legacyProjectInfoIDs B []string (card_templates.go:26)
//   projectCardTemplates B []template (card_templates.go:40)
//   cardTypeValidators  B  map (card_validation.go:23)
//   knownExecKinds      B  map (card_validation.go:123)
//   (cardstore var block) B store flags (cardstore.go:11)
//   externalSkillSources B []source (external_skill_card.go:26)
//   ignoredWatchDirs    B  map (filewatcher.go:16)
//   defaultSkipDirs     B  map (fileops.go:43)
//   errOutsideRoots     A  fmt.Errorf (project.go:1332)
//   reviewChangesetGenSem I chan struct{} semaphore (review_changeset.go:68)
//   generatedFilePatterns B []string (review_changeset.go:79)
//   ownerAgentIDLine    C  regexp (timer_workflow.go:312)
//   (wiki var block)    B  wiki card defs (wiki.go:161)
//   builtinCards        B  []*BuiltinCard (wiki.go:227)
//   obsoleteBuiltinIDs B  []string (wiki.go:238)
//   obsoletePromptCardIDs B []string (wiki.go:375)
//   builtinMountDataKeys B []string (wiki.go:572)
//   wikiTitleQueryRe    C  regexp (wiki.go:581)
//   priorityRank        B  map (wiki.go:802)
//   errWorktreeInactive A  errors.New (worktree.go:708)
//
// ── pkg/actor/puppeteditor ───────────────────────────────────────────────
//
//   nodeKindWhitelist   B  map (agent.go:32)
//   directions          B  [8][2]int (automesh/contour.go:59)
//   paramKindWhitelist  B  map (params.go:73)
//
// ── pkg/actor/shell ──────────────────────────────────────────────────────
//
//   DangerousCmds       B  map (shell.go:624)
//   builtins            B  map (shell.go:653)
//
// ── pkg/actor/sporeapp ───────────────────────────────────────────────────
//
//   capabilityHostBindings B map (sporeapp.go:242)
//
// ── pkg/actor/sshmanager ─────────────────────────────────────────────────
//
//   ansiEscapeSeq       C  regexp (sshmanager.go:1408)
//
// ── pkg/actor/user ───────────────────────────────────────────────────────
//
//   DesktopTokenIssuerKey E resource.Key (user.go:26)
//
// ── pkg/actor/voice ──────────────────────────────────────────────────────
//
//   errUnsupportedFormat A fmt.Errorf (voice.go:24)
//
// ── pkg/actor/workspace ──────────────────────────────────────────────────
//
//   crawlPollInterval   B  time.Duration (executor_crawl.go:20)
//   nativeScaffoldFS    D  embed.FS (workspace.go:159)
//   retiredBuiltinKinds B  map (workspace.go:673)
//
// ── pkg/agentkit ─────────────────────────────────────────────────────────
//
//   embeddedAssets      D  embed.FS (assets.go:15)
//   sectionOrder        B  map (cardcompiler/cardcompiler.go:163)
//   mechanismPromptFS   D  embed.FS (embed.go:11)
//   (embed var block)   D  embed.FS (embed.go:21)
//   BuiltinCards        B  []BuiltinCard (cards.go:24)
//   BuiltinCardRenames  B  map (cards.go:61)
//
// ── pkg/appdef ───────────────────────────────────────────────────────────
//
//   typeAliasRe         C  regexp (parser.go:238)
//   toolNameRe          C  regexp (validate.go:10)
//   scalars             B  map (validate.go:24)
//
// ── pkg/auth ─────────────────────────────────────────────────────────────
//
//   (jwt var blocks)    F/B JWT config + default secret (jwt.go:19,34)
//
// ── pkg/authz ────────────────────────────────────────────────────────────
//
//   defaultManager      F  auth.NewManager singleton (authz.go:10)
//
// ── pkg/builtin/demoapp ──────────────────────────────────────────────────
//
//   Manifest            B  gen.AppManifest (demoapp.go:19)
//   Modules             B  map (demoapp.go:61)
//
// ── pkg/codegen ──────────────────────────────────────────────────────────
//
//   embeddedSDKZip      D  []byte (sdk_workspace.go:18)
//   embeddedSDKChecksum B  string (sdk_workspace.go:22)
//
// ── pkg/compaction ───────────────────────────────────────────────────────
//
//   DefaultCompactionPolicy F exported mutable struct (primitives.go:66)
//
// ── pkg/config ───────────────────────────────────────────────────────────
//
//   (config var block)  F  cfg struct, exeDir (config.go:49)
//
// ── pkg/converter ────────────────────────────────────────────────────────
//
//   (converter var block) F registry map (converter.go:16)
//
// ── pkg/debug ────────────────────────────────────────────────────────────
//
//   EvalJS              G  function seam (evaljs.go:8)
//
// ── pkg/desktop ──────────────────────────────────────────────────────────
//
//   store               F  persist.Persist singleton (app.go:40)
//   (app var blocks)    B/I config + COM (app.go:45,811)
//   openDevToolsImpl    G  platform seam (devtools.go:5)
//   (devtools_windows)  I  COM defs (devtools_windows.go:10)
//   (dragout_windows)   I  COM vtables (dragout_windows.go:*)
//   (system_login)      B  login token field list + timeout constants (system_login.go:*)
//   (screenshot_windows) I Windows API (screenshot_windows.go:32)
//   b64BufPool          B  sync.Pool (wails_transport.go:74)
//   (crash_handler)     I  crash config (crash_handler.go:18)
//
// ── pkg/domain ───────────────────────────────────────────────────────────
//
//   validTurnStates     B  map (turn_lifecycle.go:50)
//   validPauseReasons   B  map (turn_lifecycle.go:59)
//   lifecycleStateByKind B map (turn_lifecycle.go:68)
//
// ── pkg/hooks ────────────────────────────────────────────────────────────
//
//   global              F  *Registry singleton (registry.go:16)
//
// ── pkg/i18n ─────────────────────────────────────────────────────────────
//
//   catalog             F  map (catalog.go:5)
//   defaultLocale       F  Locale (locale.go:16)
//
// ── pkg/llmclient ────────────────────────────────────────────────────────
//
//   cacheEphemeral      B  map (anthropic.go:17)
//   DefaultProviderGate F  *ProviderGate singleton (concurrency.go:34)
//   semPool             F  sync.Map (concurrency.go:98)
//   DefaultAggregatorHealth F health registry (health.go:80)
//   DefaultProviderHealth    F health registry (health.go:255)
//   ErrStreamClosed     A  errors.New (llmclient.go:158)
//   ErrStreamIdleTimeout A errors.New (llmclient.go:160)
//   userAgent           B  string (llmclient.go:165)
//   httpClient          B  *http.Client (llmclient.go:294)
//   (imagegen vars)     B  http.Client, userAgent, poll intervals (imagegen/vars.go, imagegen/qwen.go)
//   (mediagen vars)     B  http.Client, userAgent, pollInterval, maps (mediagen.go:*)
//   (upstream_error vars) B quota maps/slices (upstream_error.go:121+)
//
// ── pkg/persist ──────────────────────────────────────────────────────────
//
//   ErrNotExist         A  errors.New (store.go:22)
//   (fileLocks var block) F sync.Mutex + map (store.go:92)
//
// ── pkg/slashcmd ─────────────────────────────────────────────────────────
//
//   commands            F  map (registry.go:8)
//
// ── pkg/timer ────────────────────────────────────────────────────────────
//
//   once                F  sync.Once (timer.go:78)
//   ins                 F  Timer singleton (timer.go:79)
//   logger              B  timerLoggerT (logger.go:22)
//   _timeTestHandler    G  test seam (time_wheel_utils.go:12)
//   get10Ms             G  function seam (time_wheel_utils.go:19)
//
// ── pkg/util ─────────────────────────────────────────────────────────────
//
//   (platform var block) F shellCache, shellPref (platform.go:122)
//
// ── pkg/version ──────────────────────────────────────────────────────────
//
//   Version             B  string (version.go:13)
//
// ═════════════════════════════════════════════════════════════════════════
// MUTABLE SINGLETONS (Category F) — violate "Actor 优先" constraint
// ═════════════════════════════════════════════════════════════════════════
//
//   config.cfg          NO mutex  — highest priority
//   hooks.global        RWMutex   — hook registry
//   agentStore          none      — persist singleton
//   nativeToolRegistry  none      — tool registry singleton
//   authz.defaultManager none     — JWT manager singleton
//   desktop.store       none      — persist singleton
//   llmclient.DefaultProviderGate none — concurrency gate
//   llmclient.semPool   sync.Map  — semaphore pool
//   llmclient.DefaultAggregatorHealth mutex — health registry
//   llmclient.DefaultProviderHealth    mutex — health registry
//   timer.once/ins      sync.Once — timer singleton
//   i18n.catalog        none      — translation catalog
//   i18n.defaultLocale  none      — locale
//   slashcmd.commands   none      — command registry (init-only writes)
//   converter.registry  RWMutex   — converter registry
//   compaction.DefaultCompactionPolicy none — exported mutable struct
//   persist.fileLocks   sync.Mutex — file lock registry
//   util.shellCache/shellPref mutex — shell detection cache
//   auth.defaultSecret  sync.Once  — JWT secret (immutable after init)
//
// ── sporemind-plugin-sdk (EXCLUDED — ABI-bound by design) ────────────────
//
//   callableRegistry    F  mutex-protected (callable.go:20)
//   hostBridge          F  mutex-protected (bridge.go:17)
//   activeState         F  mutex-protected (plugin.go:65)
//   registeredPlugin    F  mutex-protected (plugin.go:70)
//   toolNameRe          C  regexp (manifest.go:13)
//   ErrNotImplemented   A  fmt.Errorf (manifest.go:18)
//
// ── cmd/sporecloud (EXCLUDED — separate cloud service) ──────────────
//
//   Various admin/auth/content/invite vars — not part of core runtime.
package globals_audit