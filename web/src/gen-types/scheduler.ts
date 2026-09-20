// AUTO-GENERATED - DO NOT EDIT. To regenerate:
//   make gen-schema-ts

export interface TimerSchedule {
  Cron?: string | undefined;
  Expression?: string | undefined;
  Timezone?: string | undefined;
  Enabled: boolean;
}

export interface SchedulerRegisterReq {
  ProjectID: string;
  CardID: string;
  Schedule: TimerSchedule;
}

export interface SchedulerUnregisterReq {
  ProjectID: string;
  CardID: string;
}

export interface SchedulerListReq {

}

export interface SchedulerEntryInfo {
  ProjectID: string;
  CardID: string;
  Schedule: TimerSchedule;
  NextFireAt?: string | undefined;
}

export interface SchedulerListResp {
  Entries: SchedulerEntryInfo[];
}

export interface SchedulerSetEnabledReq {
  ProjectID: string;
  CardID: string;
  Enabled: boolean;
}

export interface SchedulerSetEnabledResp {
  Enabled: boolean;
}
