// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface CardValidationError {
  Code: string;
  Field: string;
  Message: string;
}

export interface WikiValidateCardReq {
  Id: string;
  Raw: string;
}

export interface WikiValidateCardResp {
  Valid: boolean;
  Errors: CardValidationError[];
}
