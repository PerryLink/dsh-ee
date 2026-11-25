/**
 * 开启「完全放行」前的风险确认：管理员在云端机器人设置里、员工在对话页切换审批模式时共用。
 * 必须勾选「我已了解风险」才能确认；确认后请求里带 confirm_full_access，服务端也会再校验一次。
 */
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ShieldAlert } from "lucide-react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/shadcn/alert-dialog";

export function FullAccessDialog({ open, botName, onResult }: { open: boolean; botName: string; onResult: (confirmed: boolean) => void }) {
  const { t } = useTranslation();
  const [ack, setAck] = useState(false);
  // 每次打开只回报一次：点了确认之后 Radix 还会触发 onOpenChange(false)，不能再当成取消。
  const answered = useRef(false);
  useEffect(() => {
    if (open) {
      setAck(false);
      answered.current = false;
    }
  }, [open]);
  const answer = (confirmed: boolean) => {
    if (answered.current) return;
    answered.current = true;
    onResult(confirmed);
  };
  return (
    <AlertDialog open={open} onOpenChange={(next) => !next && answer(false)}>
      <AlertDialogContent data-full-access-dialog="">
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            <ShieldAlert size={18} aria-hidden className="text-red-600 dark:text-red-400" />
            {t("approval.fullDialog.title", { bot: botName })}
          </AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="space-y-2 text-sm leading-6">
              <p>{t("approval.fullDialog.body")}</p>
              <ul className="list-disc space-y-1 pl-5">
                <li>{t("approval.fullDialog.risk1")}</li>
                <li>{t("approval.fullDialog.risk2")}</li>
                <li>{t("approval.fullDialog.risk3")}</li>
              </ul>
            </div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <label className="flex items-start gap-2 text-sm text-neutral-800">
          <input type="checkbox" className="mt-1" checked={ack} onChange={(e) => setAck(e.target.checked)} />
          {t("approval.fullDialog.ack")}
        </label>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => answer(false)}>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction variant="destructive" disabled={!ack} onClick={() => answer(true)}>
            {t("approval.fullDialog.confirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
