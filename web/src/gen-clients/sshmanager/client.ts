// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
import { GosporeClient } from "@qomos/gospore-client";
import type { InvokeOptions } from "@qomos/gospore-client";
import type * as systemTypes from "../system/types";

export async function archiveExport(client: GosporeClient, req: systemTypes.SshArchiveExportReq, opts?: InvokeOptions): Promise<systemTypes.ArchiveExportResp> {
  return client.invoke<systemTypes.SshArchiveExportReq, systemTypes.ArchiveExportResp>("sshmanager.archive_export", req, { reqSchemaId: 1593, resSchemaId: 743, ...opts });
}

export const archiveExport_meta = {
  callable: "sshmanager.archive_export",
  name: "archive_export",
  reqSchemaId: 1593,
  resSchemaId: 743,
} as const;

export async function archiveImport(client: GosporeClient, req: systemTypes.SshArchiveImportReq, opts?: InvokeOptions): Promise<systemTypes.ArchiveImportResp> {
  return client.invoke<systemTypes.SshArchiveImportReq, systemTypes.ArchiveImportResp>("sshmanager.archive_import", req, { reqSchemaId: 1594, resSchemaId: 745, ...opts });
}

export const archiveImport_meta = {
  callable: "sshmanager.archive_import",
  name: "archive_import",
  reqSchemaId: 1594,
  resSchemaId: 745,
} as const;

export async function commandCreate(client: GosporeClient, req: systemTypes.SshCommandCreateReq, opts?: InvokeOptions): Promise<systemTypes.SshCommandCreateResp> {
  return client.invoke<systemTypes.SshCommandCreateReq, systemTypes.SshCommandCreateResp>("sshmanager.command_create", req, { reqSchemaId: 1585, resSchemaId: 1586, ...opts });
}

export const commandCreate_meta = {
  callable: "sshmanager.command_create",
  name: "command_create",
  reqSchemaId: 1585,
  resSchemaId: 1586,
} as const;

export async function commandList(client: GosporeClient, req: systemTypes.SshCommandListReq, opts?: InvokeOptions): Promise<systemTypes.SshCommandListResp> {
  return client.invoke<systemTypes.SshCommandListReq, systemTypes.SshCommandListResp>("sshmanager.command_list", req, { reqSchemaId: 1583, resSchemaId: 1584, ...opts });
}

export const commandList_meta = {
  callable: "sshmanager.command_list",
  name: "command_list",
  reqSchemaId: 1583,
  resSchemaId: 1584,
} as const;

export async function commandRemove(client: GosporeClient, req: systemTypes.SshCommandRemoveReq, opts?: InvokeOptions): Promise<systemTypes.SshCommandRemoveResp> {
  return client.invoke<systemTypes.SshCommandRemoveReq, systemTypes.SshCommandRemoveResp>("sshmanager.command_remove", req, { reqSchemaId: 1589, resSchemaId: 1590, ...opts });
}

export const commandRemove_meta = {
  callable: "sshmanager.command_remove",
  name: "command_remove",
  reqSchemaId: 1589,
  resSchemaId: 1590,
} as const;

export async function commandUpdate(client: GosporeClient, req: systemTypes.SshCommandUpdateReq, opts?: InvokeOptions): Promise<systemTypes.SshCommandUpdateResp> {
  return client.invoke<systemTypes.SshCommandUpdateReq, systemTypes.SshCommandUpdateResp>("sshmanager.command_update", req, { reqSchemaId: 1587, resSchemaId: 1588, ...opts });
}

export const commandUpdate_meta = {
  callable: "sshmanager.command_update",
  name: "command_update",
  reqSchemaId: 1587,
  resSchemaId: 1588,
} as const;

export async function download(client: GosporeClient, req: systemTypes.SshDownloadReq, opts?: InvokeOptions): Promise<systemTypes.SshDownloadResp> {
  return client.invoke<systemTypes.SshDownloadReq, systemTypes.SshDownloadResp>("sshmanager.download", req, { reqSchemaId: 1612, resSchemaId: 1613, ...opts });
}

export const download_meta = {
  callable: "sshmanager.download",
  name: "download",
  reqSchemaId: 1612,
  resSchemaId: 1613,
} as const;

export async function exec(client: GosporeClient, req: systemTypes.SshExecReq, opts?: InvokeOptions): Promise<systemTypes.SshExecResp> {
  return client.invoke<systemTypes.SshExecReq, systemTypes.SshExecResp>("sshmanager.exec", req, { reqSchemaId: 1595, resSchemaId: 1596, ...opts });
}

export const exec_meta = {
  callable: "sshmanager.exec",
  name: "exec",
  reqSchemaId: 1595,
  resSchemaId: 1596,
} as const;

export async function fileChmod(client: GosporeClient, req: systemTypes.SshFileChmodReq, opts?: InvokeOptions): Promise<systemTypes.SshFileChmodResp> {
  return client.invoke<systemTypes.SshFileChmodReq, systemTypes.SshFileChmodResp>("sshmanager.file_chmod", req, { reqSchemaId: 1607, resSchemaId: 1608, ...opts });
}

export const fileChmod_meta = {
  callable: "sshmanager.file_chmod",
  name: "file_chmod",
  reqSchemaId: 1607,
  resSchemaId: 1608,
} as const;

export async function fileDelete(client: GosporeClient, req: systemTypes.SshFileDeleteReq, opts?: InvokeOptions): Promise<systemTypes.SshFileDeleteResp> {
  return client.invoke<systemTypes.SshFileDeleteReq, systemTypes.SshFileDeleteResp>("sshmanager.file_delete", req, { reqSchemaId: 1565, resSchemaId: 1566, ...opts });
}

export const fileDelete_meta = {
  callable: "sshmanager.file_delete",
  name: "file_delete",
  reqSchemaId: 1565,
  resSchemaId: 1566,
} as const;

export async function fileDownload(client: GosporeClient, req: systemTypes.SshFileDownloadReq, opts?: InvokeOptions): Promise<systemTypes.SshFileDownloadResp> {
  return client.invoke<systemTypes.SshFileDownloadReq, systemTypes.SshFileDownloadResp>("sshmanager.file_download", req, { reqSchemaId: 1578, resSchemaId: 1579, ...opts });
}

export const fileDownload_meta = {
  callable: "sshmanager.file_download",
  name: "file_download",
  reqSchemaId: 1578,
  resSchemaId: 1579,
} as const;

export async function fileList(client: GosporeClient, req: systemTypes.SshFileListReq, opts?: InvokeOptions): Promise<systemTypes.SshFileListResp> {
  return client.invoke<systemTypes.SshFileListReq, systemTypes.SshFileListResp>("sshmanager.file_list", req, { reqSchemaId: 1557, resSchemaId: 1558, ...opts });
}

export const fileList_meta = {
  callable: "sshmanager.file_list",
  name: "file_list",
  reqSchemaId: 1557,
  resSchemaId: 1558,
} as const;

export async function fileMkdir(client: GosporeClient, req: systemTypes.SshFileMkdirReq, opts?: InvokeOptions): Promise<systemTypes.SshFileMkdirResp> {
  return client.invoke<systemTypes.SshFileMkdirReq, systemTypes.SshFileMkdirResp>("sshmanager.file_mkdir", req, { reqSchemaId: 1563, resSchemaId: 1564, ...opts });
}

export const fileMkdir_meta = {
  callable: "sshmanager.file_mkdir",
  name: "file_mkdir",
  reqSchemaId: 1563,
  resSchemaId: 1564,
} as const;

export async function fileRead(client: GosporeClient, req: systemTypes.SshFileReadReq, opts?: InvokeOptions): Promise<systemTypes.SshFileReadResp> {
  return client.invoke<systemTypes.SshFileReadReq, systemTypes.SshFileReadResp>("sshmanager.file_read", req, { reqSchemaId: 1559, resSchemaId: 1560, ...opts });
}

export const fileRead_meta = {
  callable: "sshmanager.file_read",
  name: "file_read",
  reqSchemaId: 1559,
  resSchemaId: 1560,
} as const;

export async function fileRename(client: GosporeClient, req: systemTypes.SshFileRenameReq, opts?: InvokeOptions): Promise<systemTypes.SshFileRenameResp> {
  return client.invoke<systemTypes.SshFileRenameReq, systemTypes.SshFileRenameResp>("sshmanager.file_rename", req, { reqSchemaId: 1567, resSchemaId: 1568, ...opts });
}

export const fileRename_meta = {
  callable: "sshmanager.file_rename",
  name: "file_rename",
  reqSchemaId: 1567,
  resSchemaId: 1568,
} as const;

export async function fileWrite(client: GosporeClient, req: systemTypes.SshFileWriteReq, opts?: InvokeOptions): Promise<systemTypes.SshFileWriteResp> {
  return client.invoke<systemTypes.SshFileWriteReq, systemTypes.SshFileWriteResp>("sshmanager.file_write", req, { reqSchemaId: 1561, resSchemaId: 1562, ...opts });
}

export const fileWrite_meta = {
  callable: "sshmanager.file_write",
  name: "file_write",
  reqSchemaId: 1561,
  resSchemaId: 1562,
} as const;

export async function fileWriteBase64(client: GosporeClient, req: systemTypes.SshFileWriteBase64Req, opts?: InvokeOptions): Promise<systemTypes.SshFileWriteBase64Resp> {
  return client.invoke<systemTypes.SshFileWriteBase64Req, systemTypes.SshFileWriteBase64Resp>("sshmanager.file_write_base64", req, { reqSchemaId: 1605, resSchemaId: 1606, ...opts });
}

export const fileWriteBase64_meta = {
  callable: "sshmanager.file_write_base64",
  name: "file_write_base64",
  reqSchemaId: 1605,
  resSchemaId: 1606,
} as const;

export async function folderCreate(client: GosporeClient, req: systemTypes.SshFolderCreateReq, opts?: InvokeOptions): Promise<systemTypes.SshFolderCreateResp> {
  return client.invoke<systemTypes.SshFolderCreateReq, systemTypes.SshFolderCreateResp>("sshmanager.folder_create", req, { reqSchemaId: 1597, resSchemaId: 1598, ...opts });
}

export const folderCreate_meta = {
  callable: "sshmanager.folder_create",
  name: "folder_create",
  reqSchemaId: 1597,
  resSchemaId: 1598,
} as const;

export async function folderRemove(client: GosporeClient, req: systemTypes.SshFolderRemoveReq, opts?: InvokeOptions): Promise<systemTypes.SshFolderRemoveResp> {
  return client.invoke<systemTypes.SshFolderRemoveReq, systemTypes.SshFolderRemoveResp>("sshmanager.folder_remove", req, { reqSchemaId: 1603, resSchemaId: 1604, ...opts });
}

export const folderRemove_meta = {
  callable: "sshmanager.folder_remove",
  name: "folder_remove",
  reqSchemaId: 1603,
  resSchemaId: 1604,
} as const;

export async function folderRename(client: GosporeClient, req: systemTypes.SshFolderRenameReq, opts?: InvokeOptions): Promise<systemTypes.SshFolderRenameResp> {
  return client.invoke<systemTypes.SshFolderRenameReq, systemTypes.SshFolderRenameResp>("sshmanager.folder_rename", req, { reqSchemaId: 1599, resSchemaId: 1600, ...opts });
}

export const folderRename_meta = {
  callable: "sshmanager.folder_rename",
  name: "folder_rename",
  reqSchemaId: 1599,
  resSchemaId: 1600,
} as const;

export async function folderReorder(client: GosporeClient, req: systemTypes.SshFolderReorderReq, opts?: InvokeOptions): Promise<systemTypes.SshFolderReorderResp> {
  return client.invoke<systemTypes.SshFolderReorderReq, systemTypes.SshFolderReorderResp>("sshmanager.folder_reorder", req, { reqSchemaId: 1601, resSchemaId: 1602, ...opts });
}

export const folderReorder_meta = {
  callable: "sshmanager.folder_reorder",
  name: "folder_reorder",
  reqSchemaId: 1601,
  resSchemaId: 1602,
} as const;

export async function historyList(client: GosporeClient, req: systemTypes.SshHistoryListReq, opts?: InvokeOptions): Promise<systemTypes.SshHistoryListResp> {
  return client.invoke<systemTypes.SshHistoryListReq, systemTypes.SshHistoryListResp>("sshmanager.history_list", req, { reqSchemaId: 1591, resSchemaId: 1592, ...opts });
}

export const historyList_meta = {
  callable: "sshmanager.history_list",
  name: "history_list",
  reqSchemaId: 1591,
  resSchemaId: 1592,
} as const;

export async function hostCreate(client: GosporeClient, req: systemTypes.SshHostCreateReq, opts?: InvokeOptions): Promise<systemTypes.SshHostCreateResp> {
  return client.invoke<systemTypes.SshHostCreateReq, systemTypes.SshHostCreateResp>("sshmanager.host_create", req, { reqSchemaId: 1539, resSchemaId: 1540, ...opts });
}

export const hostCreate_meta = {
  callable: "sshmanager.host_create",
  name: "host_create",
  reqSchemaId: 1539,
  resSchemaId: 1540,
} as const;

export async function hostList(client: GosporeClient, req: systemTypes.SshHostListReq, opts?: InvokeOptions): Promise<systemTypes.SshHostListResp> {
  return client.invoke<systemTypes.SshHostListReq, systemTypes.SshHostListResp>("sshmanager.host_list", req, { reqSchemaId: 1537, resSchemaId: 1538, ...opts });
}

export const hostList_meta = {
  callable: "sshmanager.host_list",
  name: "host_list",
  reqSchemaId: 1537,
  resSchemaId: 1538,
} as const;

export async function hostRemove(client: GosporeClient, req: systemTypes.SshHostRemoveReq, opts?: InvokeOptions): Promise<systemTypes.SshHostRemoveResp> {
  return client.invoke<systemTypes.SshHostRemoveReq, systemTypes.SshHostRemoveResp>("sshmanager.host_remove", req, { reqSchemaId: 1543, resSchemaId: 1544, ...opts });
}

export const hostRemove_meta = {
  callable: "sshmanager.host_remove",
  name: "host_remove",
  reqSchemaId: 1543,
  resSchemaId: 1544,
} as const;

export async function hostUpdate(client: GosporeClient, req: systemTypes.SshHostUpdateReq, opts?: InvokeOptions): Promise<systemTypes.SshHostUpdateResp> {
  return client.invoke<systemTypes.SshHostUpdateReq, systemTypes.SshHostUpdateResp>("sshmanager.host_update", req, { reqSchemaId: 1541, resSchemaId: 1542, ...opts });
}

export const hostUpdate_meta = {
  callable: "sshmanager.host_update",
  name: "host_update",
  reqSchemaId: 1541,
  resSchemaId: 1542,
} as const;

export async function sessionList(client: GosporeClient, req: systemTypes.SshSessionListReq, opts?: InvokeOptions): Promise<systemTypes.SshSessionListResp> {
  return client.invoke<systemTypes.SshSessionListReq, systemTypes.SshSessionListResp>("sshmanager.session_list", req, { reqSchemaId: 1546, resSchemaId: 1547, ...opts });
}

export const sessionList_meta = {
  callable: "sshmanager.session_list",
  name: "session_list",
  reqSchemaId: 1546,
  resSchemaId: 1547,
} as const;

export async function shellClose(client: GosporeClient, req: systemTypes.SshShellCloseReq, opts?: InvokeOptions): Promise<systemTypes.SshShellCloseResp> {
  return client.invoke<systemTypes.SshShellCloseReq, systemTypes.SshShellCloseResp>("sshmanager.shell_close", req, { reqSchemaId: 1550, resSchemaId: 1551, ...opts });
}

export const shellClose_meta = {
  callable: "sshmanager.shell_close",
  name: "shell_close",
  reqSchemaId: 1550,
  resSchemaId: 1551,
} as const;

export async function shellInput(client: GosporeClient, req: systemTypes.SshShellInputReq, opts?: InvokeOptions): Promise<systemTypes.SshShellInputResp> {
  return client.invoke<systemTypes.SshShellInputReq, systemTypes.SshShellInputResp>("sshmanager.shell_input", req, { reqSchemaId: 1552, resSchemaId: 1553, ...opts });
}

export const shellInput_meta = {
  callable: "sshmanager.shell_input",
  name: "shell_input",
  reqSchemaId: 1552,
  resSchemaId: 1553,
} as const;

export async function shellOpen(client: GosporeClient, req: systemTypes.SshShellOpenReq, opts?: InvokeOptions): Promise<systemTypes.SshShellOpenResp> {
  return client.invoke<systemTypes.SshShellOpenReq, systemTypes.SshShellOpenResp>("sshmanager.shell_open", req, { reqSchemaId: 1548, resSchemaId: 1549, ...opts });
}

export const shellOpen_meta = {
  callable: "sshmanager.shell_open",
  name: "shell_open",
  reqSchemaId: 1548,
  resSchemaId: 1549,
} as const;

export async function shellResize(client: GosporeClient, req: systemTypes.SshShellResizeReq, opts?: InvokeOptions): Promise<systemTypes.SshShellResizeResp> {
  return client.invoke<systemTypes.SshShellResizeReq, systemTypes.SshShellResizeResp>("sshmanager.shell_resize", req, { reqSchemaId: 1554, resSchemaId: 1555, ...opts });
}

export const shellResize_meta = {
  callable: "sshmanager.shell_resize",
  name: "shell_resize",
  reqSchemaId: 1554,
  resSchemaId: 1555,
} as const;

export async function shellRun(client: GosporeClient, req: systemTypes.SshShellRunReq, opts?: InvokeOptions): Promise<systemTypes.SshShellRunResp> {
  return client.invoke<systemTypes.SshShellRunReq, systemTypes.SshShellRunResp>("sshmanager.shell_run", req, { reqSchemaId: 1610, resSchemaId: 1611, ...opts });
}

export const shellRun_meta = {
  callable: "sshmanager.shell_run",
  name: "shell_run",
  reqSchemaId: 1610,
  resSchemaId: 1611,
} as const;

export async function *shellStream(client: GosporeClient, req: systemTypes.SshShellStreamReq, opts?: InvokeOptions): AsyncIterable<systemTypes.SshShellStreamChunk> {
  yield* client.subscribe<systemTypes.SshShellStreamChunk>("sshmanager.shell_stream", req, { reqSchemaId: 1580, chunkSchemaId: 1581, ...opts });
}

export async function statusGet(client: GosporeClient, req: systemTypes.SshStatusReq, opts?: InvokeOptions): Promise<systemTypes.SshStatusResp> {
  return client.invoke<systemTypes.SshStatusReq, systemTypes.SshStatusResp>("sshmanager.status_get", req, { reqSchemaId: 1574, resSchemaId: 1575, ...opts });
}

export const statusGet_meta = {
  callable: "sshmanager.status_get",
  name: "status_get",
  reqSchemaId: 1574,
  resSchemaId: 1575,
} as const;

export async function statusList(client: GosporeClient, req: systemTypes.SshStatusListReq, opts?: InvokeOptions): Promise<systemTypes.SshStatusListResp> {
  return client.invoke<systemTypes.SshStatusListReq, systemTypes.SshStatusListResp>("sshmanager.status_list", req, { reqSchemaId: 1576, resSchemaId: 1577, ...opts });
}

export const statusList_meta = {
  callable: "sshmanager.status_list",
  name: "status_list",
  reqSchemaId: 1576,
  resSchemaId: 1577,
} as const;

export async function tunnelClose(client: GosporeClient, req: systemTypes.SshTunnelCloseReq, opts?: InvokeOptions): Promise<systemTypes.SshTunnelCloseResp> {
  return client.invoke<systemTypes.SshTunnelCloseReq, systemTypes.SshTunnelCloseResp>("sshmanager.tunnel_close", req, { reqSchemaId: 1618, resSchemaId: 1619, ...opts });
}

export const tunnelClose_meta = {
  callable: "sshmanager.tunnel_close",
  name: "tunnel_close",
  reqSchemaId: 1618,
  resSchemaId: 1619,
} as const;

export async function tunnelList(client: GosporeClient, req: systemTypes.SshTunnelListReq, opts?: InvokeOptions): Promise<systemTypes.SshTunnelListResp> {
  return client.invoke<systemTypes.SshTunnelListReq, systemTypes.SshTunnelListResp>("sshmanager.tunnel_list", req, { reqSchemaId: 1620, resSchemaId: 1622, ...opts });
}

export const tunnelList_meta = {
  callable: "sshmanager.tunnel_list",
  name: "tunnel_list",
  reqSchemaId: 1620,
  resSchemaId: 1622,
} as const;

export async function tunnelOpen(client: GosporeClient, req: systemTypes.SshTunnelOpenReq, opts?: InvokeOptions): Promise<systemTypes.SshTunnelOpenResp> {
  return client.invoke<systemTypes.SshTunnelOpenReq, systemTypes.SshTunnelOpenResp>("sshmanager.tunnel_open", req, { reqSchemaId: 1616, resSchemaId: 1617, ...opts });
}

export const tunnelOpen_meta = {
  callable: "sshmanager.tunnel_open",
  name: "tunnel_open",
  reqSchemaId: 1616,
  resSchemaId: 1617,
} as const;

export async function upload(client: GosporeClient, req: systemTypes.SshUploadReq, opts?: InvokeOptions): Promise<systemTypes.SshUploadResp> {
  return client.invoke<systemTypes.SshUploadReq, systemTypes.SshUploadResp>("sshmanager.upload", req, { reqSchemaId: 1614, resSchemaId: 1615, ...opts });
}

export const upload_meta = {
  callable: "sshmanager.upload",
  name: "upload",
  reqSchemaId: 1614,
  resSchemaId: 1615,
} as const;

export type SshManagerEventHandler = (payload: systemTypes.SshManagerEvent) => void;

export function OnSshManagerEvent(client: GosporeClient, handler: SshManagerEventHandler): () => void {
  return client.events.onService("sshmanager", "ssh_manager_event", (payload) => handler(payload as systemTypes.SshManagerEvent));
}

export function OffSshManagerEvent(cancel: () => void): void {
  cancel();
}

