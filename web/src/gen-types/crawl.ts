// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface BrowserCrawlStartReq {
  Card?: string | undefined;
  Config?: BrowserCrawlConfig | undefined;
  Overrides?: Record<string, unknown> | undefined;
}

export interface BrowserCrawlStartResp {
  TaskId: string;
}

export interface BrowserCrawlStatusReq {
  TaskId: string;
}

export interface BrowserCrawlStatusResp {
  State: string;
  Crawled: number;
  Frontier: number;
  MaxPages: number;
  Error?: string | undefined;
}

export interface BrowserCrawlResultsReq {
  TaskId: string;
  Cursor?: number | undefined;
  Limit?: number | undefined;
}

export interface BrowserCrawlResultsResp {
  Results: BrowserCrawlPageResult[];
  NextCursor: number;
  Done: boolean;
}

export interface BrowserCrawlPageResult {
  Url: string;
  Title: string;
  Links: string[];
  Text: string;
}

export interface BrowserCrawlHandoffReq {
  TaskId: string;
}

export interface BrowserCrawlHandoffResp {
  Accepted: boolean;
  InstanceId: string;
  Url: string;
}

export interface BrowserCrawlConfig {
  Seeds?: string[] | undefined;
  MaxDepth?: number | undefined;
  MaxPages?: number | undefined;
  SameDomain?: boolean | undefined;
  RateLimit?: string | undefined;
  Mode?: string | undefined;
  Profile?: string | undefined;
  ExtractSchema?: string | undefined;
  ExtractSelectors?: Record<string, unknown> | undefined;
  InstanceID?: string | undefined;
}
