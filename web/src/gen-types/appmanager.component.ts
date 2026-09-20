// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ComponentDescriptor } from './component';

export interface AppManagerComponentListReq {

}

export interface AppManagerComponentListResp {
  Items: ComponentDescriptor[];
}

export interface AppManagerComponentGetReq {
  CardId: string;
}

export interface AppManagerComponentGetResp {
  Component: ComponentDescriptor;
}
