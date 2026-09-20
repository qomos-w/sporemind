import { useCallback, useRef, useState } from 'react'
import type { GosporeClient } from '@qomos/gospore-client'
import { useI18n } from '../../../i18n'
import { hasWailsBindings, selectAppPackageFile, selectAppPackageFolder } from '../../../application/wails-bridge'
import { extractZipText } from '../../../application/zip-reader'
import { appRegistry, normalizeApp } from '../../../application/app-registry'
import * as appmanagerClient from '../../../gen-clients/appmanager/client'
import * as filesystemClient from '../../../gen-clients/filesystem/client'
import type { AppManifest } from '../../../gen-types/app'

export interface LocalInstallState {
  previewOpen: boolean
  previewManifest: AppManifest | null
  // Desktop sources install by path (a .zip file or a package directory);
  // browser sources carry the zip bytes instead.
  previewPath: string | null
  previewPackageData: Uint8Array | null
  loading: boolean
  error: string | null
  success: string | null
}

interface PendingSource {
  path?: string
  packageData?: Uint8Array
}

const MANIFEST_ENTRY = 'app.manifest.json'

function base64ToUint8Array(base64: string): Uint8Array {
  const binary = atob(base64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}

function readFileAsArrayBuffer(file: File): Promise<ArrayBuffer> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result as ArrayBuffer)
    reader.onerror = () => reject(new Error('Failed to read file'))
    reader.readAsArrayBuffer(file)
  })
}

/**
 * Hook that drives the "install app from local source" flow:
 *
 *   1. Pick a source (desktop: a .zip file picker or a package-folder picker —
 *      Windows cannot combine both in one native dialog; browser: hidden file
 *      input for .zip).
 *   2. Parse app.manifest.json and show a preview dialog.
 *   3. On confirm, unregister any same-ID app (a plain re-register would keep
 *      stale bindings/protocol/session state), then call
 *      appmanager.install_local (desktop sends Path, browser sends PackageData).
 *   4. On success, refresh the app registry.
 */
export function useLocalAppInstall() {
  const { t } = useI18n()
  const [state, setState] = useState<LocalInstallState>({
    previewOpen: false,
    previewManifest: null,
    previewPath: null,
    previewPackageData: null,
    loading: false,
    error: null,
    success: null,
  })
  const fileInputRef = useRef<HTMLInputElement>(null)
  const pendingSourceRef = useRef<PendingSource | null>(null)

  const reset = useCallback(() => {
    pendingSourceRef.current = null
    setState({
      previewOpen: false,
      previewManifest: null,
      previewPath: null,
      previewPackageData: null,
      loading: false,
      error: null,
      success: null,
    })
  }, [])

  const closePreview = useCallback(() => {
    setState(prev => ({ ...prev, previewOpen: false, error: null }))
  }, [])

  const parsePackageAndPreview = useCallback(async (data: ArrayBuffer, path?: string) => {
    let packageData: Uint8Array
    let manifestText: string
    try {
      packageData = new Uint8Array(data)
      manifestText = await extractZipText(data, MANIFEST_ENTRY)
    } catch (err) {
      setState(prev => ({
        ...prev,
        loading: false,
        error: t('installPreview.error.parse'),
      }))
      return
    }

    let manifest: AppManifest
    try {
      manifest = JSON.parse(manifestText) as AppManifest
    } catch {
      setState(prev => ({
        ...prev,
        loading: false,
        error: t('installPreview.error.parse'),
      }))
      return
    }

    pendingSourceRef.current = path ? { path } : { packageData }
    setState(prev => ({
      ...prev,
      loading: false,
      previewOpen: true,
      previewManifest: manifest,
      previewPath: path ?? null,
      previewPackageData: path ? null : packageData,
      error: null,
    }))
  }, [t])

  const previewDirectory = useCallback(async (client: GosporeClient, dirPath: string) => {
    let manifest: AppManifest
    try {
      const resp = await filesystemClient.read(client, { Path: `${dirPath}/${MANIFEST_ENTRY}` })
      manifest = JSON.parse(resp.Content) as AppManifest
    } catch {
      setState(prev => ({
        ...prev,
        loading: false,
        error: t('installPreview.error.parse'),
      }))
      return
    }
    pendingSourceRef.current = { path: dirPath }
    setState(prev => ({
      ...prev,
      loading: false,
      previewOpen: true,
      previewManifest: manifest,
      previewPath: dirPath,
      previewPackageData: null,
      error: null,
    }))
  }, [t])

  // Windows IFileDialog cannot pick files and folders in one dialog, so the
  // desktop flow needs an explicit source kind. Zip path: file picker filtered
  // to .zip. Folder path: directory picker for an unpacked package.
  const selectAndPreviewZip = useCallback(async (client: GosporeClient) => {
    setState(prev => ({ ...prev, loading: true, error: null, success: null }))

    if (hasWailsBindings()) {
      const sel = await selectAppPackageFile()
      if (!sel || !sel.Path) {
        setState(prev => ({ ...prev, loading: false }))
        return
      }
      try {
        const resp = await filesystemClient.readBase64(client, { Path: sel.Path })
        const data = base64ToUint8Array(resp.Content)
        await parsePackageAndPreview(data.buffer, sel.Path)
      } catch (err) {
        setState(prev => ({
          ...prev,
          loading: false,
          error: err instanceof Error ? err.message : t('installPreview.error.read'),
        }))
      }
      return
    }

    // Browser: open the hidden file input. Its onChange handler will drive the
    // rest of the flow via handleBrowserFileSelect.
    setState(prev => ({ ...prev, loading: false }))
    fileInputRef.current?.click()
  }, [parsePackageAndPreview, t])

  const selectAndPreviewFolder = useCallback(async (client: GosporeClient) => {
    if (!hasWailsBindings()) return
    setState(prev => ({ ...prev, loading: true, error: null, success: null }))
    const sel = await selectAppPackageFolder()
    if (!sel || !sel.Path) {
      setState(prev => ({ ...prev, loading: false }))
      return
    }
    await previewDirectory(client, sel.Path)
  }, [previewDirectory, t])

  const handleBrowserFileSelect = useCallback(
    async (file: File) => {
      setState(prev => ({ ...prev, loading: true, error: null, success: null }))
      try {
        const data = await readFileAsArrayBuffer(file)
        await parsePackageAndPreview(data)
      } catch (err) {
        setState(prev => ({
          ...prev,
          loading: false,
          error: err instanceof Error ? err.message : t('installPreview.error.read'),
        }))
      }
    },
    [parsePackageAndPreview, t],
  )

  // Zip drag-and-drop entry (launcher panel drop): same parse+preview flow as
  // handleBrowserFileSelect. The dropped file has no host-absolute path, so the
  // preview path stays null and install goes through PackageData bytes.
  const handleDroppedZip = useCallback(
    async (file: File) => {
      setState(prev => ({ ...prev, loading: true, error: null, success: null }))
      try {
        const data = await readFileAsArrayBuffer(file)
        await parsePackageAndPreview(data)
      } catch (err) {
        setState(prev => ({
          ...prev,
          loading: false,
          error: err instanceof Error ? err.message : t('installPreview.error.read'),
        }))
      }
    },
    [parsePackageAndPreview, t],
  )

  const finishInstallSuccess = useCallback((status: any, message: string) => {
    appRegistry.upsert(normalizeApp(status))
    setState(prev => ({
      ...prev,
      previewOpen: false,
      loading: false,
      success: message,
    }))
  }, [])

  const pendingSource = useCallback((): PendingSource | null => {
    const refSource = pendingSourceRef.current
    if (refSource?.path || refSource?.packageData) return refSource
    if (state.previewPath) return { path: state.previewPath }
    if (state.previewPackageData) return { packageData: state.previewPackageData }
    return null
  }, [state.previewPath, state.previewPackageData])

  const installFromSource = useCallback(
    async (client: GosporeClient, source: PendingSource) => {
      const req = source.path
        ? { Path: source.path }
        : { PackageData: source.packageData }
      const resp = await appmanagerClient.installLocal(client, req)
      finishInstallSuccess(resp.Status, `${resp.Status.Id} ${t('installPreview.success')}`)
    },
    [finishInstallSuccess, t],
  )

  const confirmInstall = useCallback(
    async (client: GosporeClient) => {
      const source = pendingSource()
      if (!source) return
      setState(prev => ({ ...prev, loading: true, error: null }))
      try {
        // Same-ID reinstall must fully drop the old registration first:
        // unregister removes agent bindings, protocol entries and session
        // state that a plain re-register would leave behind, then the fresh
        // install registers and loads cleanly.
        const appId = state.previewManifest?.Id
        if (appId && appRegistry.get(appId)) {
          await appmanagerClient.unregister(client, { Id: appId })
          appRegistry.remove(appId)
        }
        await installFromSource(client, source)
      } catch (err) {
        setState(prev => ({
          ...prev,
          loading: false,
          error: err instanceof Error ? err.message : t('installPreview.error.install'),
        }))
      }
    },
    [installFromSource, pendingSource, state.previewManifest, t],
  )

  return {
    state,
    fileInputRef,
    selectAndPreviewZip,
    selectAndPreviewFolder,
    handleBrowserFileSelect,
    handleDroppedZip,
    confirmInstall,
    closePreview,
    reset,
  }
}
