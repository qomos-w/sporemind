// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface WebFetchReq {
  Url: string;
  MaxChars?: number | undefined;
}

export interface WebFetchMeta {
  Description: string;
  SiteName: string;
  Lang: string;
}

export interface WebFetchResp {
  Url: string;
  Title: string;
  Text: string;
  Meta: WebFetchMeta;
  Truncated: boolean;
}

export interface WebDownloadReq {
  Url: string;
  SavePath: string;
  MaxBytes?: number | undefined;
}

export interface WebDownloadResp {
  SavedPath: string;
  BytesDownloaded: number;
  ContentType: string;
  Truncated: boolean;
}
