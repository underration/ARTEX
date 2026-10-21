"use client";

import { useEffect, useRef, useState } from "react";

import { useRouter } from "next/navigation";

import { AlertTriangle, ShieldCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

export default function LoginPage() {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);
  const [agreed, setAgreed] = useState(false);
  const [termsOpen, setTermsOpen] = useState(false);
  const [readToEnd, setReadToEnd] = useState(false);
  const termsBodyRef = useRef<HTMLDivElement>(null);

  // 滚动到条款底部（含无需滚动即可完整展示的情况）方可点击「同意」。
  function handleTermsScroll() {
    const el = termsBodyRef.current;
    if (!el) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 8) setReadToEnd(true);
  }

  useEffect(() => {
    if (!termsOpen) return;
    // 打开时重置，并处理内容本就不足一屏、无法触发滚动的场景。
    setReadToEnd(false);
    const el = termsBodyRef.current;
    if (el && el.scrollHeight <= el.clientHeight + 8) setReadToEnd(true);
  }, [termsOpen]);

  useEffect(() => {
    // 已登录直接进主界面（静态导出下无 middleware 代劳这层跳转）。
    const token = auth.getToken();
    if (token) {
      // localStorage 可能仍有凭据但 cookie 已丢失。先同步，再发起全新请求，
      // 避免服务端守卫或路由缓存把跳转送回仍处于 checking 状态的登录页。
      auth.setToken(token);
      window.location.replace("/function/tasks");
      return;
    }
    api
      .authStatus()
      .then(({ initialized }) => {
        if (!initialized) router.replace("/setup");
      })
      .catch(() => setError("バックエンドサービスに接続できません"))
      .finally(() => setChecking(false));
  }, [router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!agreed) {
      setError("「利用規約」を読んで同意してください");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.login("ARTEX", password);
      auth.setToken(token);
      window.location.replace("/function/tasks");
    } catch {
      setError("ユーザー名またはパスワードが正しくありません");
    } finally {
      setLoading(false);
    }
  }

  if (checking) {
    return (
      <div role="status" className="flex min-h-dvh items-center justify-center text-muted-foreground">
        ログイン状態を確認中…
      </div>
    );
  }

  return (
    <div className="flex h-dvh">
      {/* Left panel */}
      <div className="hidden flex-col items-center justify-center bg-primary p-12 text-center lg:flex lg:w-1/3">
        <div className="relative flex items-center justify-center">
          <div className="absolute size-80 rounded-full border border-primary-foreground/10" />
          <div className="absolute size-60 rounded-full border border-primary-foreground/15" />
          <div className="absolute size-40 rounded-full border border-primary-foreground/20" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="ARTEX" width={160} height={160} className="relative brightness-0 invert" />
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="text-2xl font-medium tracking-tight">ログイン</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">おかえりなさい。ARTEXを続行するにはパスワードを入力してください</p>
          </div>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="username">ユーザー名</Label>
              <Input id="username" value="ARTEX" readOnly className="bg-muted text-muted-foreground" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">パスワード</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="パスワードを入力"
                autoFocus
                autoComplete="current-password"
              />
            </div>
            <div className="flex items-start gap-2">
              <Checkbox
                id="agree-terms"
                checked={agreed}
                onCheckedChange={(v) => setAgreed(v === true)}
                className="mt-0.5"
              />
              <Label htmlFor="agree-terms" className="text-sm font-normal leading-relaxed text-muted-foreground">
                <button
                  type="button"
                  onClick={() => setTermsOpen(true)}
                  className="mr-0.5 font-medium text-primary underline-offset-4 hover:underline"
                >
                  《利用規約》
                </button>
                を読み、同意しました
              </Label>
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading || !password || !agreed}>
              {loading ? "ログイン中..." : "ログイン"}
            </Button>
          </form>
        </div>
      </div>

      <Dialog open={termsOpen} onOpenChange={setTermsOpen}>
        <DialogContent className="gap-0 p-0 sm:max-w-2xl">
          <DialogHeader className="flex-row items-center gap-3 border-b px-6 py-4">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <ShieldCheck className="size-5" />
            </div>
            <div className="space-y-0.5">
              <DialogTitle className="text-base">ARTEX 利用規約および免責事項</DialogTitle>
              <p className="text-xs text-muted-foreground">
                バージョン v1.0 · 発効日 2026-09-18 · ログイン前に以下のすべての条項をお読みください
              </p>
            </div>
          </DialogHeader>

          <div
            ref={termsBodyRef}
            onScroll={handleTermsScroll}
            className="max-h-[60vh] space-y-5 overflow-y-auto px-6 py-5 text-sm leading-relaxed text-muted-foreground"
          >
            <p className="rounded-lg border bg-muted/40 p-3 text-foreground/80">
              本「利用規約および免責事項」（以下「本規約」）は、ユーザーと ARTEX
              プロジェクトの作成者および貢献者との間における本ソフトウェアの利用に関する合意事項を定めるものです。ご利用の前に各条項の内容、特に太字で強調された免責、責任制限および禁止事項を十分にご確認ください。
              <span className="font-medium text-foreground">
                {" "}
                本ソフトウェアをダウンロード、インストール、アクセス、またはいかなる方法で利用した場合も、本規約のすべての内容を読み、理解し、同意したものとみなされます。
              </span>
            </p>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  1
                </span>
                第1条 · 定義とオープンソースライセンス
              </h4>
              <p className="pl-7">
                本ソフトウェア（ARTEX）は、GNU Affero General Public License
                v3.0（AGPL-3.0）に基づき公開されているオープンソースプログラムです。利用者は同ライセンスに基づき本ソフトウェアを自由に使用、複製、改変、再頒布できますが、派生物（ネットワーク経由で第三者に提供するオンラインサービスを含む）も同様に
                AGPL-3.0 ライセンスの下でオープンソース化し、対応する完全なソースコードを利用者に公開する必要があります。AGPL-3.0 の完全な条項は、付属の LICENSE ファイルに従うものとします。
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  2
                </span>
                第2条 · 許可される利用範囲
              </h4>
              <p className="pl-7">
                本ソフトウェアは、個人の学習、コード研究、セキュリティ技術の検証、および自身が構築したローカルの隔離環境での技術検証を目的として提供されており、学習、学術研究、コードレビューなどの非攻撃的・非破壊的な用途にのみ適用されます。本条で明示的に許可された場合を除き、その他のいかなる目的にも本ソフトウェアを使用してはなりません。
              </p>
            </section>

            <section className="space-y-2">
              <h4 className="flex items-center gap-2 font-medium text-destructive">
                <span className="flex size-5 items-center justify-center rounded-md bg-destructive/10 text-xs font-semibold text-destructive">
                  3
                </span>
                <AlertTriangle className="size-4" />
                第3条 · 禁止事項
              </h4>
              <ul className="ml-7 list-decimal space-y-1.5 rounded-lg border border-destructive/20 bg-destructive/5 p-3 pl-8 text-foreground/80 marker:text-destructive/70">
                <li>
                  ウェブサイト、オンラインサービス、第三者が所有するネットワークシステムに対するスキャン、プロービング、エクスプロイト、または攻撃を行うこと（許可の有無や自身が所有する資産であるかを問いません）；
                </li>
                <li>本ソフトウェアを実際のペネトレーションテスト、攻撃・防御の対抗演習、レッド・ブルーチーム演習、または本番環境で使用すること；</li>
                <li>本ソフトウェアを不正アクセス、データ窃取、ランサムウェア、サービス妨害（DoS/DDoS）、またはあらゆる破壊的・犯罪的活動に使用すること；</li>
                <li>本ソフトウェアおよびその出力に含まれる著作権、ライセンス、またはセキュリティ警告情報を削除、改ざん、または回避すること；</li>
                <li>居住国または地域の法令、規制、監督規定に違反するいかなる行為を行うこと。</li>
              </ul>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  4
                </span>
                第4条 · 知的財産権
              </h4>
              <p className="pl-7">
                本ソフトウェアの著作権および関連する知的財産権は、プロジェクトの作成者および貢献者に帰属し、AGPL-3.0
                ライセンスで定められた範囲内で対応する権利が利用者に付与されます。同ライセンスで明示的に付与された権利を除き、本規約によって明示的または黙示的にその他の権利が付与されることはありません。
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  5
                </span>
                第5条 · データとプライバシー
              </h4>
              <p className="pl-7">
                本ソフトウェアはセルフホスト可能なオープンソースプログラムであり、作成者は集中型サービスを運営しておらず、利用者の利用データを収集またはアップロードすることもありません。利用過程で生成、処理、または接触するすべてのデータは利用者が自ら管理し、その適法性と安全性に責任を負うものとします。不適切なデータ処理によって生じたすべての結果は、利用者が自己の責任で負うものとします。
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  6
                </span>
                第6条 · コンプライアンスと法的責任
              </h4>
              <p className="pl-7">
                利用者は、サイバーセキュリティ、データセキュリティ、個人情報保護、コンピュータ犯罪等に関する居住国または地域のすべての法令を遵守するものとします。
                <span className="font-medium text-foreground">
                  {" "}
                  利用者が上記の法令または本規約に違反したことにより生じるすべての法的責任および結果は、利用者本人が単独で負うものとし、本ソフトウェアの作成者および貢献者は一切の責任を負いません。
                </span>
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  7
                </span>
                第7条 · 免責事項と責任の制限
              </h4>
              <p className="pl-7">
                本ソフトウェアは「現状有姿（AS IS）」かつ「提供可能な限度（AS
                AVAILABLE）」で提供され、商品性、特定目的への適合性、正確性、および権利非侵害の保証を含むがこれらに限定されない、明示または黙示を問わずいかなる保証も伴いません。適用法令で認められる最大限の範囲において、本ソフトウェアの作成者および貢献者は、本ソフトウェアの使用または使用不能（使用方法の適否を問わない）に起因する直接的、間接的、付随的、特別、または派生的な損害（データ損失、システム破損、事業の中断、逸失利益、法的手続きを含むがこれらに限定されない）について、一切の責任を負いません。
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  8
                </span>
                第8条 · 条項の変更と最終解釈
              </h4>
              <p className="pl-7">
                作成者は法令やプロジェクトの開発ニーズに応じて本規約を随時更新する権利を有し、更新されたバージョンはプロジェクトと共に公開され、公開日から効力を生じます。利用者が本ソフトウェアの利用を継続した場合、改定後の条項に同意したものとみなされます。法律で認められる範囲において、本規約の最終的な解釈権はプロジェクトの作成者に帰属します。本規約のいずれかの条項が無効と判断された場合でも、他の条項の効力には影響しません。
              </p>
            </section>
          </div>

          <DialogFooter className="mx-0 mb-0 flex-col items-stretch gap-2 rounded-b-xl px-6 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-xs text-muted-foreground">
              {readToEnd ? "すべての条項を確認しました" : "条項を最後までスクロールして確認してください"}
            </p>
            <DialogClose asChild>
              <Button
                type="button"
                disabled={!readToEnd}
                onClick={() => {
                  setAgreed(true);
                  setError("");
                }}
              >
                すべての条項を読み、同意しました
              </Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
