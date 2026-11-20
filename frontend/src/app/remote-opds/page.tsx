import { Fragment, useEffect, useState, type FormEvent } from "react";
import { apiPath } from "@/lib/base-path";
import { useLocale } from "@/lib/i18n";
import { useAuth } from "@/lib/auth-context";
import { fetchLibraries, type Library } from "@/api/libraries";
import { notifyLibraryAccessChanged } from "@/hooks/useComicList";

type CatalogLink = { url: string; kind: "catalog" | "acquisition"; title?: string };
type CatalogEntry = { title: string; links: CatalogLink[] };
type Catalog = { title: string; next?: string; entries: CatalogEntry[] };
type CatalogLogin = { origin: string; username: string; password: string };

function isWebURL(value: unknown): value is string {
  if (typeof value !== "string") return false;
  try {
    const url = new URL(value);
    return (url.protocol === "https:" || url.protocol === "http:") && !url.username && !url.password;
  } catch {
    return false;
  }
}

function isCatalogLink(value: unknown): value is CatalogLink {
  return value !== null && typeof value === "object" && "url" in value && isWebURL(value.url) &&
    "kind" in value && (value.kind === "catalog" || value.kind === "acquisition") &&
    (!("title" in value) || typeof value.title === "string");
}

function isEntry(value: unknown): value is CatalogEntry {
  return value !== null && typeof value === "object" && "title" in value && typeof value.title === "string" &&
    "links" in value && Array.isArray(value.links) && value.links.every(isCatalogLink);
}

function isCatalog(value: unknown): value is Catalog {
  return value !== null && typeof value === "object" && "title" in value && typeof value.title === "string" &&
    (!("next" in value) || isWebURL(value.next)) &&
    "entries" in value && Array.isArray(value.entries) && value.entries.every(isEntry);
}

async function postOPDS(path: string, url: string, login: CatalogLogin | null, destination?: { libraryId: string; rootPath: string }): Promise<unknown> {
  const credentials = login?.origin === new URL(url).origin ? { username: login.username, password: login.password } : {};
  const response = await fetch(apiPath(path), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url, ...credentials, ...destination }),
  });
  const data: unknown = await response.json();
  if (!response.ok) {
    throw new Error(data && typeof data === "object" && "error" in data && typeof data.error === "string" ? data.error : "");
  }
  return data;
}

const inputStyle = "w-full rounded-lg border border-border bg-background px-3 py-2";
const buttonStyle = "rounded-lg border border-border px-3 py-2 text-sm hover:bg-background disabled:opacity-50";

export default function RemoteOPDSPage({ embedded = false }: { embedded?: boolean }) {
  const { locale } = useLocale();
  const zh = locale === "zh-CN";
  const { user } = useAuth();
  const canImport = user?.role === "admin";
  const [address, setAddress] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [login, setLogin] = useState<CatalogLogin | null>(null);
  const [history, setHistory] = useState<Catalog[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [libraries, setLibraries] = useState<Library[]>([]);
  const [librariesFailed, setLibrariesFailed] = useState(false);
  const [folder, setFolder] = useState("");
  const [importing, setImporting] = useState(false);
  const [saved, setSaved] = useState("");
  const catalog = history[history.length - 1];
  const hasCatalog = Boolean(catalog);
  const folders = libraries.filter((library) => library.enabled).flatMap((library) =>
    [...new Set([library.rootPath, ...(library.rootPaths || [])])].filter(Boolean).map((rootPath) => ({
      key: JSON.stringify([library.id, rootPath]), libraryId: library.id, rootPath, label: `${library.name} — ${rootPath}`,
    })));
  const destination = folders.find((item) => item.key === folder);

  useEffect(() => {
    if (!hasCatalog || !canImport) return;
    void fetchLibraries().then(setLibraries, () => setLibrariesFailed(true));
  }, [hasCatalog, canImport]);

  async function load(url: string, credentials: CatalogLogin | null, start = false) {
    setLoading(true);
    setError("");
    setSaved("");
    const fallback = zh ? "无法加载 OPDS 目录。" : "Could not load the OPDS catalog.";
    try {
      const data = await postOPDS("/remote-opds", url, credentials);
      if (!isCatalog(data)) {
        setError(fallback);
        return;
      }
      if (start) setLogin(credentials);
      setHistory((previous) => start ? [data] : [...previous, data]);
    } catch (failure) {
      setError(failure instanceof Error && failure.message ? `${fallback} ${failure.message}` : fallback);
    } finally {
      setLoading(false);
    }
  }

  function connect(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const url = address.trim();
    if (!isWebURL(url)) {
      setError(zh ? "请输入有效的 HTTP(S) 目录地址，不要在地址中填写凭据。" : "Enter a valid HTTP(S) catalog URL without embedded credentials.");
      return;
    }
    void load(url, username || password ? { origin: new URL(url).origin, username, password } : null, true);
  }

  async function importBook(url: string) {
    if (!canImport || !destination || importing) return;
    setImporting(true);
    setSaved("");
    setError("");
    const fallback = zh ? "无法下载到书库。" : "Could not download to the library.";
    try {
      const data = await postOPDS("/remote-opds/import", url, login, { libraryId: destination.libraryId, rootPath: destination.rootPath });
      if (!data || typeof data !== "object" || !("filename" in data) || typeof data.filename !== "string") throw new Error();
      const scanned = "scanCompleted" in data && data.scanCompleted === true;
      if (scanned) notifyLibraryAccessChanged();
      setSaved(zh ? `已保存 ${data.filename}。${scanned ? "书库扫描已完成。" : "请手动扫描书库以更新书架。"}`
        : `Saved ${data.filename}. ${scanned ? "Library scan completed." : "Scan the library to refresh the bookshelf."}`);
    } catch (failure) {
      setError(failure instanceof Error && failure.message ? `${fallback} ${failure.message}` : fallback);
    } finally {
      setImporting(false);
    }
  }

  const Container = embedded ? "section" : "main";
  const Heading = embedded ? "h2" : "h1";
  return (
    <Container className={embedded ? "space-y-5" : "mx-auto max-w-4xl space-y-5 px-4 pb-24 pt-8 sm:px-8"}>
      <Heading className={embedded ? "hidden text-xl font-semibold sm:block" : "text-2xl font-semibold"}>{zh ? "网络 OPDS" : "Remote OPDS"}</Heading>
      <p className="text-sm text-muted">{zh ? "浏览公网 OPDS 1.x 目录。需要登录时，请使用 HTTPS。" : "Browse public OPDS 1.x catalogs. Use HTTPS if sign-in is required."}</p>
      <form className="space-y-3 rounded-xl border border-border bg-surface p-4" onSubmit={connect} autoComplete="off">
        <label className="block space-y-1">
          <span className="text-sm">{zh ? "目录地址" : "Catalog URL"}</span>
          <input required type="url" className={inputStyle} value={address} onChange={(e) => setAddress(e.target.value)} placeholder="https://example.com/opds" />
        </label>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="block space-y-1">
            <span className="text-sm">{zh ? "远程用户名（可选）" : "Remote username (optional)"}</span>
            <input className={inputStyle} value={username} onChange={(e) => setUsername(e.target.value)} />
          </label>
          <label className="block space-y-1">
            <span className="text-sm">{zh ? "远程密码或 API 密钥（可选）" : "Remote password or API key (optional)"}</span>
            <input type="password" className={inputStyle} value={password} onChange={(e) => setPassword(e.target.value)} />
          </label>
        </div>
        <button className={buttonStyle} disabled={loading || importing}>{zh ? "连接" : "Connect"}</button>
      </form>
      {loading && <p role="status">{zh ? "正在加载目录…" : "Loading catalog…"}</p>}
      {importing && <p role="status">{zh ? "正在下载到书库…" : "Downloading to library…"}</p>}
      {saved && <p role="status">{saved}</p>}
      {error && <p role="alert" className="text-red-500">{error}</p>}
      {catalog && (
        <section className="space-y-3" aria-busy={loading || importing}>
          {canImport && (
            <>
              <label className="block space-y-1">
                <span className="text-sm">{zh ? "下载目标书库文件夹" : "Destination library folder"}</span>
                <select className={inputStyle} value={folder} disabled={importing} onChange={(event) => setFolder(event.target.value)}>
                  <option value="">{zh ? "选择已配置的文件夹" : "Choose a configured folder"}</option>
                  {folders.map((item) => <option key={item.key} value={item.key}>{item.label}</option>)}
                </select>
              </label>
              {librariesFailed && <p role="alert">{zh ? "无法读取书库列表。" : "Could not load libraries."}</p>}
            </>
          )}
          <div className="flex items-center gap-3">
            {history.length > 1 && <button className={buttonStyle} disabled={loading || importing} onClick={() => {
              setHistory((previous) => previous.slice(0, -1));
              setError("");
            }}>{zh ? "返回" : "Back"}</button>}
            <h2 className="break-all text-xl font-semibold">{catalog.title || "OPDS"}</h2>
          </div>
          {catalog.entries.length === 0 && <p className="text-sm text-muted">{zh ? "此目录没有条目。" : "This catalog has no entries."}</p>}
          {catalog.entries.length > 0 && <ul className="divide-y divide-border rounded-xl border border-border bg-surface">
            {catalog.entries.map((entry, index) => (
              <li key={index} className="space-y-2 p-3">
                <p className="break-all font-medium">{entry.title || (zh ? "未命名条目" : "Untitled entry")}</p>
                <div className="flex flex-wrap gap-2">
                  {entry.links.map((link, linkIndex) => link.kind === "catalog" ? (
                    <button key={linkIndex} className={buttonStyle} disabled={loading || importing} onClick={() => void load(link.url, login)}>{link.title || (zh ? "打开目录" : "Open catalog")}</button>
                  ) : (
                    <Fragment key={linkIndex}>
                      <a className={buttonStyle} href={link.url} target="_blank" rel="noreferrer">{link.title || (zh ? "在来源站打开 / 下载" : "Open / download from source")}</a>
                      {canImport && <button className={buttonStyle} disabled={!destination || loading || importing} onClick={() => void importBook(link.url)}>{zh ? "下载到书库" : "Download to library"}</button>}
                    </Fragment>
                  ))}
                </div>
              </li>
            ))}
          </ul>}
          {catalog.next && <button className={buttonStyle} disabled={loading || importing} onClick={() => {
            if (catalog.next) void load(catalog.next, login);
          }}>{zh ? "下一页" : "Next page"}</button>}
        </section>
      )}
    </Container>
  );
}
