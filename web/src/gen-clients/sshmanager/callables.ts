// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen

import { CallableRegistry, type CallableEntry } from "@qomos/spore-ts/callables";

export const callableEntries: CallableEntry[] = [
  {
    namespace: "sshmanager",
    name: "archive_export",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1593,
    finalSchemaId: 743,
    req: {
      kind: "struct",
      name: "SshArchiveExportReq",
      className: "SshArchiveExportReq"
    },
    final: {
      kind: "struct",
      name: "ArchiveExportResp",
      className: "ArchiveExportResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "archive_import",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1594,
    finalSchemaId: 745,
    req: {
      kind: "struct",
      name: "SshArchiveImportReq",
      className: "SshArchiveImportReq"
    },
    final: {
      kind: "struct",
      name: "ArchiveImportResp",
      className: "ArchiveImportResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "command_create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1585,
    finalSchemaId: 1586,
    req: {
      kind: "struct",
      name: "SshCommandCreateReq",
      className: "SshCommandCreateReq"
    },
    final: {
      kind: "struct",
      name: "SshCommandCreateResp",
      className: "SshCommandCreateResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "command_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1583,
    finalSchemaId: 1584,
    req: {
      kind: "struct",
      name: "SshCommandListReq",
      className: "SshCommandListReq"
    },
    final: {
      kind: "struct",
      name: "SshCommandListResp",
      className: "SshCommandListResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "command_remove",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1589,
    finalSchemaId: 1590,
    req: {
      kind: "struct",
      name: "SshCommandRemoveReq",
      className: "SshCommandRemoveReq"
    },
    final: {
      kind: "struct",
      name: "SshCommandRemoveResp",
      className: "SshCommandRemoveResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "command_update",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1587,
    finalSchemaId: 1588,
    req: {
      kind: "struct",
      name: "SshCommandUpdateReq",
      className: "SshCommandUpdateReq"
    },
    final: {
      kind: "struct",
      name: "SshCommandUpdateResp",
      className: "SshCommandUpdateResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "download",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1612,
    finalSchemaId: 1613,
    req: {
      kind: "struct",
      name: "SshDownloadReq",
      className: "SshDownloadReq"
    },
    final: {
      kind: "struct",
      name: "SshDownloadResp",
      className: "SshDownloadResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "exec",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1595,
    finalSchemaId: 1596,
    req: {
      kind: "struct",
      name: "SshExecReq",
      className: "SshExecReq"
    },
    final: {
      kind: "struct",
      name: "SshExecResp",
      className: "SshExecResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_chmod",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1607,
    finalSchemaId: 1608,
    req: {
      kind: "struct",
      name: "SshFileChmodReq",
      className: "SshFileChmodReq"
    },
    final: {
      kind: "struct",
      name: "SshFileChmodResp",
      className: "SshFileChmodResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_delete",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1565,
    finalSchemaId: 1566,
    req: {
      kind: "struct",
      name: "SshFileDeleteReq",
      className: "SshFileDeleteReq"
    },
    final: {
      kind: "struct",
      name: "SshFileDeleteResp",
      className: "SshFileDeleteResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_download",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1578,
    finalSchemaId: 1579,
    req: {
      kind: "struct",
      name: "SshFileDownloadReq",
      className: "SshFileDownloadReq"
    },
    final: {
      kind: "struct",
      name: "SshFileDownloadResp",
      className: "SshFileDownloadResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1557,
    finalSchemaId: 1558,
    req: {
      kind: "struct",
      name: "SshFileListReq",
      className: "SshFileListReq"
    },
    final: {
      kind: "struct",
      name: "SshFileListResp",
      className: "SshFileListResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_mkdir",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1563,
    finalSchemaId: 1564,
    req: {
      kind: "struct",
      name: "SshFileMkdirReq",
      className: "SshFileMkdirReq"
    },
    final: {
      kind: "struct",
      name: "SshFileMkdirResp",
      className: "SshFileMkdirResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_read",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1559,
    finalSchemaId: 1560,
    req: {
      kind: "struct",
      name: "SshFileReadReq",
      className: "SshFileReadReq"
    },
    final: {
      kind: "struct",
      name: "SshFileReadResp",
      className: "SshFileReadResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_rename",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1567,
    finalSchemaId: 1568,
    req: {
      kind: "struct",
      name: "SshFileRenameReq",
      className: "SshFileRenameReq"
    },
    final: {
      kind: "struct",
      name: "SshFileRenameResp",
      className: "SshFileRenameResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_write",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1561,
    finalSchemaId: 1562,
    req: {
      kind: "struct",
      name: "SshFileWriteReq",
      className: "SshFileWriteReq"
    },
    final: {
      kind: "struct",
      name: "SshFileWriteResp",
      className: "SshFileWriteResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "file_write_base64",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1605,
    finalSchemaId: 1606,
    req: {
      kind: "struct",
      name: "SshFileWriteBase64Req",
      className: "SshFileWriteBase64Req"
    },
    final: {
      kind: "struct",
      name: "SshFileWriteBase64Resp",
      className: "SshFileWriteBase64Resp"
    }
  },
  {
    namespace: "sshmanager",
    name: "folder_create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1597,
    finalSchemaId: 1598,
    req: {
      kind: "struct",
      name: "SshFolderCreateReq",
      className: "SshFolderCreateReq"
    },
    final: {
      kind: "struct",
      name: "SshFolderCreateResp",
      className: "SshFolderCreateResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "folder_remove",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1603,
    finalSchemaId: 1604,
    req: {
      kind: "struct",
      name: "SshFolderRemoveReq",
      className: "SshFolderRemoveReq"
    },
    final: {
      kind: "struct",
      name: "SshFolderRemoveResp",
      className: "SshFolderRemoveResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "folder_rename",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1599,
    finalSchemaId: 1600,
    req: {
      kind: "struct",
      name: "SshFolderRenameReq",
      className: "SshFolderRenameReq"
    },
    final: {
      kind: "struct",
      name: "SshFolderRenameResp",
      className: "SshFolderRenameResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "folder_reorder",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1601,
    finalSchemaId: 1602,
    req: {
      kind: "struct",
      name: "SshFolderReorderReq",
      className: "SshFolderReorderReq"
    },
    final: {
      kind: "struct",
      name: "SshFolderReorderResp",
      className: "SshFolderReorderResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "history_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1591,
    finalSchemaId: 1592,
    req: {
      kind: "struct",
      name: "SshHistoryListReq",
      className: "SshHistoryListReq"
    },
    final: {
      kind: "struct",
      name: "SshHistoryListResp",
      className: "SshHistoryListResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "host_create",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1539,
    finalSchemaId: 1540,
    req: {
      kind: "struct",
      name: "SshHostCreateReq",
      className: "SshHostCreateReq"
    },
    final: {
      kind: "struct",
      name: "SshHostCreateResp",
      className: "SshHostCreateResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "host_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1537,
    finalSchemaId: 1538,
    req: {
      kind: "struct",
      name: "SshHostListReq",
      className: "SshHostListReq"
    },
    final: {
      kind: "struct",
      name: "SshHostListResp",
      className: "SshHostListResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "host_remove",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1543,
    finalSchemaId: 1544,
    req: {
      kind: "struct",
      name: "SshHostRemoveReq",
      className: "SshHostRemoveReq"
    },
    final: {
      kind: "struct",
      name: "SshHostRemoveResp",
      className: "SshHostRemoveResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "host_update",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1541,
    finalSchemaId: 1542,
    req: {
      kind: "struct",
      name: "SshHostUpdateReq",
      className: "SshHostUpdateReq"
    },
    final: {
      kind: "struct",
      name: "SshHostUpdateResp",
      className: "SshHostUpdateResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "session_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1546,
    finalSchemaId: 1547,
    req: {
      kind: "struct",
      name: "SshSessionListReq",
      className: "SshSessionListReq"
    },
    final: {
      kind: "struct",
      name: "SshSessionListResp",
      className: "SshSessionListResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "shell_close",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1550,
    finalSchemaId: 1551,
    req: {
      kind: "struct",
      name: "SshShellCloseReq",
      className: "SshShellCloseReq"
    },
    final: {
      kind: "struct",
      name: "SshShellCloseResp",
      className: "SshShellCloseResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "shell_input",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1552,
    finalSchemaId: 1553,
    req: {
      kind: "struct",
      name: "SshShellInputReq",
      className: "SshShellInputReq"
    },
    final: {
      kind: "struct",
      name: "SshShellInputResp",
      className: "SshShellInputResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "shell_open",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1548,
    finalSchemaId: 1549,
    req: {
      kind: "struct",
      name: "SshShellOpenReq",
      className: "SshShellOpenReq"
    },
    final: {
      kind: "struct",
      name: "SshShellOpenResp",
      className: "SshShellOpenResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "shell_resize",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1554,
    finalSchemaId: 1555,
    req: {
      kind: "struct",
      name: "SshShellResizeReq",
      className: "SshShellResizeReq"
    },
    final: {
      kind: "struct",
      name: "SshShellResizeResp",
      className: "SshShellResizeResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "shell_run",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1610,
    finalSchemaId: 1611,
    req: {
      kind: "struct",
      name: "SshShellRunReq",
      className: "SshShellRunReq"
    },
    final: {
      kind: "struct",
      name: "SshShellRunResp",
      className: "SshShellRunResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "shell_stream",
    visibility: "admin",
    mode: "streaming",
    reqSchemaId: 1580,
    chunkSchemaId: 1581,
    finalSchemaId: 0,
    req: {
      kind: "struct",
      name: "SshShellStreamReq",
      className: "SshShellStreamReq"
    },
    chunk: {
      kind: "struct",
      name: "SshShellStreamChunk",
      className: "SshShellStreamChunk"
    },
    final: {
      kind: "void",
      name: "void"
    }
  },
  {
    namespace: "sshmanager",
    name: "status_get",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1574,
    finalSchemaId: 1575,
    req: {
      kind: "struct",
      name: "SshStatusReq",
      className: "SshStatusReq"
    },
    final: {
      kind: "struct",
      name: "SshStatusResp",
      className: "SshStatusResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "status_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1576,
    finalSchemaId: 1577,
    req: {
      kind: "struct",
      name: "SshStatusListReq",
      className: "SshStatusListReq"
    },
    final: {
      kind: "struct",
      name: "SshStatusListResp",
      className: "SshStatusListResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "tunnel_close",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1618,
    finalSchemaId: 1619,
    req: {
      kind: "struct",
      name: "SshTunnelCloseReq",
      className: "SshTunnelCloseReq"
    },
    final: {
      kind: "struct",
      name: "SshTunnelCloseResp",
      className: "SshTunnelCloseResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "tunnel_list",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1620,
    finalSchemaId: 1622,
    req: {
      kind: "struct",
      name: "SshTunnelListReq",
      className: "SshTunnelListReq"
    },
    final: {
      kind: "struct",
      name: "SshTunnelListResp",
      className: "SshTunnelListResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "tunnel_open",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1616,
    finalSchemaId: 1617,
    req: {
      kind: "struct",
      name: "SshTunnelOpenReq",
      className: "SshTunnelOpenReq"
    },
    final: {
      kind: "struct",
      name: "SshTunnelOpenResp",
      className: "SshTunnelOpenResp"
    }
  },
  {
    namespace: "sshmanager",
    name: "upload",
    visibility: "admin",
    mode: "unary",
    reqSchemaId: 1614,
    finalSchemaId: 1615,
    req: {
      kind: "struct",
      name: "SshUploadReq",
      className: "SshUploadReq"
    },
    final: {
      kind: "struct",
      name: "SshUploadResp",
      className: "SshUploadResp"
    }
  },
];

export function buildCallableRegistry(): CallableRegistry {
  const registry = new CallableRegistry();
  for (const entry of callableEntries) {
    registry.register(entry);
  }
  return registry;
}
