// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface BrowserCookieEntry {
  Name: string;
  Value: string;
  Domain: string;
  Path: string;
  Expires: number;
  HttpOnly: boolean;
  Secure: boolean;
  SameSite: string;
}

export interface BrowserManagerExportCookiesReq {
  Id: string;
}

export interface BrowserManagerExportCookiesResp {
  Cookies: Record<string, BrowserCookieEntry[]>;
}

export interface BrowserManagerImportCookiesReq {
  Id: string;
  Cookies: Record<string, BrowserCookieEntry[]>;
}

export interface BrowserManagerImportCookiesResp {
  Imported: number;
}
