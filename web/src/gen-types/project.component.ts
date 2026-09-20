// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

import { ComponentDescriptor } from './component';

export interface ProjectComponentListReq {

}

export interface ProjectComponentListResp {
  Items: ComponentDescriptor[];
}

export interface ProjectComponentGetReq {
  CardId: string;
}

export interface ProjectComponentGetResp {
  Component: ComponentDescriptor;
}
