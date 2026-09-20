import type { ExtensionClass } from "./core/ExtensionManager";

export interface MenuItemDef {
  name: string;
  label: string;
  keywords?: string;
}

/**
 * Global registry for custom card component nodes and slash-menu items.
 *
 * Usage:
 *   import { CardNodeRegistry } from "@/ui/editor";
 *   CardNodeRegistry.register(MyCustomNode, {
 *     menuItem: { label: "Custom Block", keywords: "custom widget" },
 *   });
 *
 * All registered extensions are automatically merged into RichCardEditor's
 * default schema (on top of basicExtensions). Registered menu items appear in
 * the slash (`/`) menu.
 */
class CardNodeRegistryImpl {
  private extensions = new Map<string, ExtensionClass>();
  private menuItems = new Map<string, MenuItemDef>();

  /**
   * Register a custom extension (Node, Mark, or ReactNode subclass).
   * The extension's `name` is used as the key — re-registering the same name
   * replaces the previous registration.
   *
   * @param ext      The extension class to register.
   * @param options  Optional: add a slash-menu entry for this extension.
   */
  register(
    ext: ExtensionClass,
    options?: { menuItem?: Omit<MenuItemDef, "name"> },
  ): void {
    const name = this.deriveName(ext);
    this.extensions.set(name, ext);
    if (options?.menuItem) {
      this.menuItems.set(name, { name, ...options.menuItem });
    }
  }

  /** Unregister a previously registered extension by its node/mark name. */
  unregister(name: string): void {
    this.extensions.delete(name);
    this.menuItems.delete(name);
  }

  /** Returns all registered extension classes (for merging into the schema). */
  getExtensions(): ExtensionClass[] {
    return [...this.extensions.values()];
  }

  /** Returns all registered slash-menu items. */
  getMenuItems(): MenuItemDef[] {
    return [...this.menuItems.values()];
  }

  /** Clear all registrations (useful for tests). */
  clear(): void {
    this.extensions.clear();
    this.menuItems.clear();
  }

  private deriveName(ext: ExtensionClass): string {
    const instance = new (ext as new () => unknown)() as { name?: string };
    return instance.name || ext.name || "unnamed";
  }
}

export const CardNodeRegistry = new CardNodeRegistryImpl();
