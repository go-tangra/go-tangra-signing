declare module 'shell/vben/common-ui' {
  import type { Component, DefineComponent } from 'vue';
  export const Page: DefineComponent<any, any, any>;
  export function useVbenDrawer(options?: any): [Component, any];
  export function useVbenModal(options?: any): [Component, any];
  export type VbenFormProps = any;
}

declare module 'shell/vben/icons' {
  import type { Component } from 'vue';
  export const LucideCheck: Component;
  export const LucideClock: Component;
  export const LucideDownload: Component;
  export const LucideEye: Component;
  export const LucideFileSignature: Component;
  export const LucideKey: Component;
  export const LucideMail: Component;
  export const LucidePencil: Component;
  export const LucidePenTool: Component;
  export const LucideLayoutGrid: Component;
  export const LucidePlus: Component;
  export const LucideSave: Component;
  export const LucideSend: Component;
  export const LucideShieldCheck: Component;
  export const LucideTrash: Component;
  export const LucideUpload: Component;
  export const LucideX: Component;
  export const LucideXCircle: Component;
  export const LucideType: Component;
  export const LucideHash: Component;
  export const LucideLetterText: Component;
  export const LucideCalendar: Component;
  export const LucideImage: Component;
  export const LucidePaperclip: Component;
  export const LucideList: Component;
  export const LucideCheckSquare: Component;
  export const LucideCheckCheck: Component;
  export const LucideCircleDot: Component;
  export const LucideColumns3: Component;
  export const LucideStamp: Component;
  export const LucideCreditCard: Component;
  export const LucideGripVertical: Component;
  export const LucideSettings: Component;
}

declare module 'shell/vben/stores' {
  import type { StoreDefinition } from 'pinia';
  export const useAccessStore: StoreDefinition;
}

declare module 'shell/vben/layouts' {
  import type { Component } from 'vue';
  export const BasicLayout: Component;
}

declare module 'shell/app-layout' {
  import type { Component } from 'vue';
  const component: Component;
  export default component;
}

declare module 'shell/adapter/vxe-table' {
  import type { DefineComponent } from 'vue';
  export function useVbenVxeGrid(options: any): [DefineComponent<any, any, any>, any];
  export type VxeGridProps<T = any> = any;
}

declare module 'shell/locales' {
  export function $t(key: string, ...args: any[]): string;
}
