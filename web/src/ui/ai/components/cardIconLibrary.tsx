import type { LucideIcon } from 'lucide-react'
import { createLucideIcon } from 'lucide-react'
import {
  AlertCircle, Archive, BarChart3, Bell, Binary, BookOpen, Bookmark, Bug, Calendar, Camera, CheckCircle2, CheckSquare, Circle, CircleDot, Clock, Cloud, Code2, Coffee, Coins, Compass, Copy, CornerUpLeft, Cpu, CreditCard, Crown, Database, DollarSign, Download, Edit3, Eye, FileArchive, FileAudio, FileBox, FileCode, FileImage, FileJson, FileKey, FileLock, FileMinus, FilePlus, FileQuestion, FileSearch, FileText, FileVideo, FileX, Files, Filter, Flag, Flame, Folder, FolderGit, FolderKanban, FolderOpen, GitBranch, GitCommit, GitMerge, GitPullRequest, Globe, Grid3X3, GraduationCap, HardDrive, Hash, Heart, HelpCircle, History, Home, Image, Info, Joystick, Key, Layers, LayoutDashboard, LayoutGrid, Lightbulb, Link, Link2, Lock, Mail, Map, MapPin, Megaphone, Menu, MessageCircle, MessageSquare, Mic, Monitor, Moon, MoreHorizontal, MousePointerClick, Music, Network, Newspaper, NotebookPen, Package, Palette, Paperclip, Pencil, Percent, Phone, Pin, Plane, Play, Plug, Puzzle, QrCode, Radio, RefreshCw, Rocket, RotateCcw, Rss, Save, Scan, Scissors, Search, Send, Server, Settings, Share2, Shield, ShieldAlert, ShieldCheck, ShoppingBag, Siren, Smartphone, Smile, Sparkles, SquareAsterisk, Star, Store, Sun, Tablet, Tag, Tags, Target, Terminal, ThumbsDown, ThumbsUp, Timer, Trash2, TreeDeciduous, Trophy, Truck, Tv, Umbrella, Unlock, Upload, User, UserCheck, UserPlus, Users, Variable, Video, Wallet, Wand2, Watch, Waypoints, Webhook, Wifi, Wrench, Zap,
} from 'lucide-react'
import { CARD_ICON_CATALOG, type CardIconCategory } from './cardIconCatalog'

export type { CardIconCategory } from './cardIconCatalog'

export interface CardIconDefinition {
  name: string
  label: string
  keywords: string[]
  category: CardIconCategory
  component: LucideIcon
}

// lucide-react has no spider glyph, so the crawl bundle's icon is defined
// locally in lucide stroke style.
const Spider = createLucideIcon('Spider', [
  ['circle', { cx: '12', cy: '8', r: '2', key: 'sp-1' }],
  ['circle', { cx: '12', cy: '15.5', r: '3.5', key: 'sp-2' }],
  ['path', { d: 'M10.3 6.8 7 4 4 5', key: 'sp-3' }],
  ['path', { d: 'M9.9 9.3 6 8.5 3.5 11', key: 'sp-4' }],
  ['path', { d: 'M9.7 13 5.5 13.5 4 17', key: 'sp-5' }],
  ['path', { d: 'M10 17l-2 3 .5 2', key: 'sp-6' }],
  ['path', { d: 'M13.7 6.8 17 4l3 1', key: 'sp-7' }],
  ['path', { d: 'M14.1 9.3 18 8.5l2.5 2.5', key: 'sp-8' }],
  ['path', { d: 'M14.3 13l4.2.5L20 17', key: 'sp-9' }],
  ['path', { d: 'm14 17 2 3-.5 2', key: 'sp-10' }],
])

// Components in the exact order of CARD_ICON_CATALOG entries.
const CARD_ICON_COMPONENTS: LucideIcon[] = [
  // Task
  Target, CheckCircle2, CheckSquare, Calendar, Clock, Star, Flag, Pin, Bookmark, Circle, CircleDot, SquareAsterisk, Trophy, Timer,
  // Status
  Zap, AlertCircle, Flame, Rocket, Lightbulb, HelpCircle, Info, Bell, ShieldAlert, ShieldCheck, Siren,
  // Content
  BookOpen, FileText, Folder, FolderOpen, FolderKanban, FolderGit, Archive, Pencil, Edit3, NotebookPen, Newspaper, Files, FilePlus, FileMinus, FileSearch, FileCode, FileJson, FileKey, FileLock, FileQuestion, FileX, FileArchive, FileBox, FileImage, FileAudio, FileVideo,
  // Development
  Code2, Database, Bug, Spider, Terminal, GitBranch, GitCommit, GitMerge, GitPullRequest, Cpu, HardDrive, Server, Cloud, Binary, Variable, Layers, Puzzle, Wrench, Plug, Network, Waypoints,
  // Collaboration
  Users, User, UserPlus, UserCheck, CornerUpLeft, MessageCircle, MessageSquare, Mail, Send, Link, Link2, Share2, Phone, Video, GraduationCap,
  // System
  Settings, Search, Lock, Unlock, Shield, Globe, Package, Palette, Home, LayoutGrid, LayoutDashboard, Grid3X3, Menu, MoreHorizontal, Filter, RefreshCw, RotateCcw, History, Trash2, Save, Download, Upload, Copy, Scissors, Paperclip, Tag, Tags, Hash, BarChart3, Wifi, Sun, Moon, Eye, Scan, QrCode, Key, Crown, Sparkles, Wand2, Webhook, Radio, Rss, Megaphone, Store, ShoppingBag, CreditCard, DollarSign, Coins, Wallet, Percent, Plane, Truck, Map, MapPin, Compass, Umbrella, TreeDeciduous, Heart, Smile, ThumbsUp, ThumbsDown, Coffee, Joystick, MousePointerClick,
  // Media
  Image, Camera, Music, Mic, Play, Monitor, Smartphone, Tablet, Watch, Tv,
]

if (CARD_ICON_COMPONENTS.length !== CARD_ICON_CATALOG.length) {
  throw new Error(`card icon catalog/component mismatch: ${CARD_ICON_CATALOG.length} catalog entries vs ${CARD_ICON_COMPONENTS.length} components`)
}

export const CARD_ICON_DEFINITIONS: CardIconDefinition[] = CARD_ICON_CATALOG.map(([name, label, keywords, category], index) => ({
  name,
  label,
  keywords,
  category,
  component: CARD_ICON_COMPONENTS[index]!,
}))

export const CARD_ICON_LIBRARY: Record<string, LucideIcon> = Object.fromEntries(
  CARD_ICON_DEFINITIONS.map(definition => [definition.name, definition.component]),
)

export function isLucideIconName(name: string): boolean {
  return name in CARD_ICON_LIBRARY
}

export const CARD_ICON_CATEGORIES: CardIconCategory[] = ['task', 'content', 'development', 'collaboration', 'status', 'system', 'media']
