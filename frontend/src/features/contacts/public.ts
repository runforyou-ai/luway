/** contacts 对外入口：供宿主与其他 feature 复用的页面、档案与工作状态。 */
export { ContactsPage } from "./contacts-page"
export {
  ContactProfileEditor,
  ContactProfileGridRow,
} from "./contact-profile-editor"
export {
  selectableWorkStatuses,
  WorkStatusBadge,
  WorkStatusDot,
  WorkStatusPicker,
  workStatusLabel,
} from "./work-status"
export { ContactForm } from "./external/contact-form"
export { contactStageKey, contactStageOptions } from "./external/contact-labels"
export {
  contactUpdateInput,
  contactValuesFromDetail,
  useContactSchema,
  useNewContactSchema,
  type ContactFormValues,
} from "./external/contact-schema"
