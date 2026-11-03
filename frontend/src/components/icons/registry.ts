import ArchiveIcon from './artwork/archive'
import ArrowDownIcon from './artwork/arrow-down'
import ArrowDownUpIcon from './artwork/arrow-down-up'
import ArrowLeftIcon from './artwork/arrow-left'
import ArrowLeftRightIcon from './artwork/arrow-left-right'
import ArrowRightIcon from './artwork/arrow-right'
import ArrowUpIcon from './artwork/arrow-up'
import AudioLinesIcon from './artwork/audio-lines'
import BadgeCheckIcon from './artwork/badge-check'
import BanIcon from './artwork/ban'
import BellIcon from './artwork/bell'
import BookOpenIcon from './artwork/book-open'
import BoxIcon from './artwork/box'
import BracesIcon from './artwork/braces'
import BrainIcon from './artwork/brain'
import BrushIcon from './artwork/brush'
import CalculatorIcon from './artwork/calculator'
import CalendarIcon from './artwork/calendar'
import ChartColumnIncreasingIcon from './artwork/chart-column-increasing'
import ChartNoAxesColumnIncreasingIcon from './artwork/chart-no-axes-column-increasing'
import CheckIcon from './artwork/check'
import ChevronDownIcon from './artwork/chevron-down'
import ChevronLeftIcon from './artwork/chevron-left'
import ChevronRightIcon from './artwork/chevron-right'
import ChevronUpIcon from './artwork/chevron-up'
import ChevronsUpDownIcon from './artwork/chevrons-up-down'
import CircleIcon from './artwork/circle'
import CircleAlertIcon from './artwork/circle-alert'
import CircleCheckIcon from './artwork/circle-check'
import CircleDollarSignIcon from './artwork/circle-dollar-sign'
import CircleHelpIcon from './artwork/circle-help'
import CircleUserIcon from './artwork/circle-user'
import CircleXIcon from './artwork/circle-x'
import ClipboardIcon from './artwork/clipboard'
import ClockIcon from './artwork/clock'
import CloudIcon from './artwork/cloud'
import Columns3Icon from './artwork/columns-3'
import CopyIcon from './artwork/copy'
import CpuIcon from './artwork/cpu'
import CreditCardIcon from './artwork/credit-card'
import CrownIcon from './artwork/crown'
import DatabaseIcon from './artwork/database'
import DownloadIcon from './artwork/download'
import EllipsisIcon from './artwork/ellipsis'
import EraserIcon from './artwork/eraser'
import ExternalLinkIcon from './artwork/external-link'
import EyeIcon from './artwork/eye'
import EyeOffIcon from './artwork/eye-off'
import FileTextIcon from './artwork/file-text'
import FlameIcon from './artwork/flame'
import FlaskIcon from './artwork/flask'
import FolderIcon from './artwork/folder'
import FunnelIcon from './artwork/funnel'
import GiftIcon from './artwork/gift'
import GlobeIcon from './artwork/globe'
import GripVerticalIcon from './artwork/grip-vertical'
import HandshakeIcon from './artwork/handshake'
import HistoryIcon from './artwork/history'
import HomeIcon from './artwork/home'
import ImageIcon from './artwork/image'
import InboxIcon from './artwork/inbox'
import InfoIcon from './artwork/info'
import KeyIcon from './artwork/key'
import LanguagesIcon from './artwork/languages'
import LayersIcon from './artwork/layers'
import LayoutDashboardIcon from './artwork/layout-dashboard'
import LightbulbIcon from './artwork/lightbulb'
import LinkIcon from './artwork/link'
import ListIcon from './artwork/list'
import ListOrderedIcon from './artwork/list-ordered'
import LoaderCircleIcon from './artwork/loader-circle'
import LockKeyholeIcon from './artwork/lock-keyhole'
import LogInIcon from './artwork/log-in'
import LogOutIcon from './artwork/log-out'
import MailIcon from './artwork/mail'
import MenuIcon from './artwork/menu'
import MessageCircleIcon from './artwork/message-circle'
import MessageSquareIcon from './artwork/message-square'
import MinimizeIcon from './artwork/minimize'
import MinusIcon from './artwork/minus'
import MonitorIcon from './artwork/monitor'
import MoonIcon from './artwork/moon'
import MoveRightIcon from './artwork/move-right'
import PaletteIcon from './artwork/palette'
import PaperclipIcon from './artwork/paperclip'
import PlayIcon from './artwork/play'
import PlusIcon from './artwork/plus'
import PowerIcon from './artwork/power'
import ReceiptIcon from './artwork/receipt'
import RefreshCwIcon from './artwork/refresh-cw'
import SearchIcon from './artwork/search'
import ServerIcon from './artwork/server'
import SettingsIcon from './artwork/settings'
import ShieldIcon from './artwork/shield'
import ShieldCheckIcon from './artwork/shield-check'
import ShieldXIcon from './artwork/shield-x'
import SlidersHorizontalIcon from './artwork/sliders-horizontal'
import SparklesIcon from './artwork/sparkles'
import SquareIcon from './artwork/square'
import SquareDashedIcon from './artwork/square-dashed'
import SquarePenIcon from './artwork/square-pen'
import StoreIcon from './artwork/store'
import SunIcon from './artwork/sun'
import TagIcon from './artwork/tag'
import TagsIcon from './artwork/tags'
import TerminalIcon from './artwork/terminal'
import ThermometerIcon from './artwork/thermometer'
import TicketIcon from './artwork/ticket'
import TrashIcon from './artwork/trash'
import TrendingDownIcon from './artwork/trending-down'
import TrendingUpIcon from './artwork/trending-up'
import TriangleAlertIcon from './artwork/triangle-alert'
import TrophyIcon from './artwork/trophy'
import TypeIcon from './artwork/type'
import Undo2Icon from './artwork/undo-2'
import UploadIcon from './artwork/upload'
import UserIcon from './artwork/user'
import UserPlusIcon from './artwork/user-plus'
import UsersIcon from './artwork/users'
import VideoIcon from './artwork/video'
import WalletIcon from './artwork/wallet'
import WrenchIcon from './artwork/wrench'
import XIcon from './artwork/x'
import ZapIcon from './artwork/zap'
import CalendarPlusIcon from './artwork/calendar-plus'
import SendIcon from './artwork/send'
import PuzzleIcon from './artwork/puzzle'
import type { IconDefinition } from './types'

// registry.ts maps semantic icon names to the ported Lucide artwork files.
// Pages and components render Icon.vue with one of these names, so changing the
// artwork behind a name updates every caller that uses it.

// icons maps application names to imported artwork definitions; multiple
// semantic names can intentionally share one renderer.
export const icons = {
  crown: CrownIcon, // Exclusive access emblem.
  play: PlayIcon,
  refresh: RefreshCwIcon,
  edit: SquarePenIcon,
  trash: TrashIcon,
  plus: PlusIcon,
  search: SearchIcon,
  more: EllipsisIcon,
  chart: ChartColumnIncreasingIcon,
  clock: ClockIcon,
  link: LinkIcon,
  sync: RefreshCwIcon,
  chevronDown: ChevronDownIcon,
  chevronRight: ChevronRightIcon,
  chevronLeft: ChevronLeftIcon,
  check: CheckIcon,
  x: XIcon,
  eye: EyeIcon,
  eyeOff: EyeOffIcon,
  cog: SettingsIcon,
  grid: LayoutDashboardIcon,
  chat: MessageCircleIcon,
  lightbulb: LightbulbIcon,
  arrowRight: ArrowRightIcon,
  arrowLeft: ArrowLeftIcon,
  arrowUp: ArrowUpIcon,
  arrowDown: ArrowDownIcon,
  arrowsUpDown: ArrowDownUpIcon,
  chevronUp: ChevronUpIcon,
  externalLink: ExternalLinkIcon,
  checkCircle: CircleCheckIcon,
  xCircle: CircleXIcon,
  exclamationCircle: CircleAlertIcon,
  exclamationTriangle: TriangleAlertIcon,
  infoCircle: InfoIcon,
  questionCircle: CircleHelpIcon,
  user: UserIcon,
  userCircle: CircleUserIcon,
  userPlus: UserPlusIcon,
  users: UsersIcon,
  document: FileTextIcon,
  clipboard: ClipboardIcon,
  copy: CopyIcon,
  inbox: InboxIcon,
  modalityText: TypeIcon,
  modalityImage: ImageIcon,
  modalityAudio: AudioLinesIcon,
  moveRight: MoveRightIcon,
  download: DownloadIcon,
  upload: UploadIcon,
  filter: FunnelIcon,
  globe: GlobeIcon,
  languages: LanguagesIcon,
  sort: ChevronsUpDownIcon,
  key: KeyIcon,
  lock: LockKeyholeIcon,
  shield: ShieldIcon,
  menu: MenuIcon,
  calendar: CalendarIcon,
  calendarPlus: CalendarPlusIcon,
  send: SendIcon,
  puzzle: PuzzleIcon,
  home: HomeIcon,
  terminal: TerminalIcon,
  gift: GiftIcon,
  creditCard: CreditCardIcon,
  mail: MailIcon,
  chartBar: ChartNoAxesColumnIncreasingIcon,
  trendingUp: TrendingUpIcon,
  database: DatabaseIcon,
  cube: BoxIcon,
  bell: BellIcon,
  bolt: ZapIcon,
  sparkles: SparklesIcon,
  cloud: CloudIcon,
  server: ServerIcon,
  sun: SunIcon,
  moon: MoonIcon,
  monitor: MonitorIcon,
  book: BookOpenIcon,
  power: PowerIcon,
  dollar: CircleDollarSignIcon,
  ban: BanIcon,
  login: LogInIcon,
  swap: ArrowLeftRightIcon,
  beaker: FlaskIcon,
  cpu: CpuIcon,
  chatBubble: MessageSquareIcon,
  calculator: CalculatorIcon,
  fire: FlameIcon,
  badge: BadgeCheckIcon,
  brain: BrainIcon,
  loader: LoaderCircleIcon,
  dashboard: LayoutDashboardIcon,
  modelMarketplace: StoreIcon,
  ranking: TrophyIcon,
  creative: PaletteIcon,
  affiliate: HandshakeIcon,
  folder: FolderIcon,
  pricing: TagsIcon,
  recharge: WalletIcon,
  order: ReceiptIcon,
  orderList: ListOrderedIcon,
  ticket: TicketIcon,
  shieldCheck: ShieldCheckIcon,
  logout: LogOutIcon,
  minus: MinusIcon,
  columns: Columns3Icon,
  list: ListIcon,
  minimize: MinimizeIcon,
  trendingDown: TrendingDownIcon,
  grip: GripVerticalIcon,
  tag: TagIcon,
  undo: Undo2Icon,
  archive: ArchiveIcon,
  layers: LayersIcon,
  shieldX: ShieldXIcon,
  circle: CircleIcon,
  square: SquareIcon,
  selection: SquareDashedIcon,
  brush: BrushIcon,
  eraser: EraserIcon,
  sliders: SlidersHorizontalIcon,
  modalityVideo: VideoIcon,
  modalityPdf: FileTextIcon,
  history: HistoryIcon,
  toolCall: WrenchIcon,
  structuredOutput: BracesIcon,
  temperature: ThermometerIcon,
  attachment: PaperclipIcon,
} as const satisfies Record<string, IconDefinition>

/** IconName lists the semantic names accepted by Icon.vue. */
export type IconName = keyof typeof icons
