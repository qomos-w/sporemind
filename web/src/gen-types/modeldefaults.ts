// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface ModelDefault {
  Prefix: string;
  MaxContextLength: number;
  MaxTokens?: number | undefined;
  Modality?: string | undefined;
  CostInput?: number | undefined;
  CostOutput?: number | undefined;
}

export interface AIManagerModelDefaultsGetReq {

}

export interface AIManagerModelDefaultsGetResp {
  Items: ModelDefault[];
}

export interface AIManagerModelDefaultsSetReq {
  Items: ModelDefault[];
}

export interface AIManagerModelDefaultsSetResp {
  Ok: boolean;
  Error: string;
}

export interface AIManagerFetchOpenRouterModelsReq {

}

export interface AIManagerFetchOpenRouterModelsResp {
  Items: ModelDefault[];
}
