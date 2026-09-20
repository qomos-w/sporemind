// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface EntitlementsView {
  Features: Record<string, boolean>;
  RateLimitTier: string;
  Tier: string;
  Pass: PassView;
  IsExperimentalSubscriber: boolean;
  Credits: number;
}

export interface CloudAccountStatus {
  Linked: boolean;
  AccountId: string;
  DisplayName?: string | undefined;
  AvatarUrl?: string | undefined;
  RateLimitTier?: string | undefined;
  Tier: string;
  IsExperimentalSubscriber: boolean;
  EntitlementsDegraded: boolean;
  TokenExpiresAt?: string | undefined;
  EntitlementsFetchedAt?: string | undefined;
}

export interface CloudAccountLinkReq {
  AccountId: string;
  AccessToken: string;
  RefreshToken: string;
  ExpiresIn: number;
  DisplayName?: string | undefined;
  AvatarUrl?: string | undefined;
}

export interface CloudAccountUnlinkResp {

}

export interface CloudAccountGetEntitlementsResp {
  Entitlements: EntitlementsView;
  Degraded: boolean;
}

export interface ContentListItem {
  Id: string;
  ContentType: string;
  Name: string;
  Slug: string;
  Description?: string | undefined;
  Version: string;
  Author?: string | undefined;
  DownloadCount: number;
  Sha256: string;
  Signature?: string | undefined;
  Visibility: string;
  Pricing: string;
}

export interface ContentSearchReq {
  ContentType?: string | undefined;
  Query?: string | undefined;
  Sort?: string | undefined;
  Page?: number | undefined;
  PageSize?: number | undefined;
}

export interface ContentSearchResp {
  Items: ContentListItem[];
  Page: number;
  PageSize: number;
  Total: number;
}

export interface ContentDetailReq {
  Slug: string;
}

export interface ContentDetailResp {
  Item: ContentListItem;
  Manifest: string;
}

export interface ContentInstallReq {
  Slug: string;
  Confirm: boolean;
}

export interface ContentInstallResp {
  Slug: string;
  ContentType: string;
  Status: string;
  Message: string;
  Risk: ContentRiskAssessment;
}

export interface ContentRiskAssessment {
  ContentType: string;
  SignaturePresent: boolean;
  Sha256Verified: boolean;
  EntryPoint: string;
  Warnings: string[];
}

export interface PassView {
  Type: string;
  StartsAt?: string | undefined;
  EndsAt?: string | undefined;
}

export interface CdkeyRedeemReq {
  Code: string;
}

export interface CdkeyRedeemResp {
  ProductName: string;
  PeriodDays: number;
  StartsAt: string;
  EndsAt: string;
}

export interface CloudAccountSessionTokenReq {

}

export interface CloudAccountSessionTokenResp {
  AccessToken: string;
  ExpiresAt: string;
}
