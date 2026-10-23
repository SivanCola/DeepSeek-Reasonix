import { t } from "../../i18n";

export type Remembered = "session" | "always";

const LABEL: Record<Remembered, string> = {
  session: "本会话允许此类操作，不再询问",
  always: "始终允许此类操作，不再询问",
};

/** The quieter half of a prompt's footer: the choices that answer this question
 *  and every later one of its kind. Which of them is drawn is the kernel's call
 *  (`options` names what it will honour here), and the line under them says where
 *  each one is kept, so remembering is never a surprise. One component so every
 *  card that offers it words and places it the same way. */
export function RememberChoice({ options, target, busy, disabled, onChoose }: {
  options: readonly Remembered[];
  target: string;
  busy: string;
  disabled: boolean;
  onChoose: (choice: Remembered) => void;
}) {
  if (options.length === 0) return null;
  return (
    <>
      {options.map((choice) => (
        <button key={choice} className="btn" data-weak="" data-action="decision.tool" data-target={target} data-value={choice}
          aria-describedby={`remember-where-${target}`} disabled={disabled} onClick={() => onChoose(choice)}>
          {busy === choice ? t("正在提交…") : t(LABEL[choice])}
        </button>
      ))}
    </>
  );
}

/** Where the remembered choices live, as one sentence for the footer's note. */
export function RememberWhere({ options, target }: { options: readonly Remembered[]; target: string }) {
  if (options.length === 0) return null;
  const text = options.includes("always")
    ? options.includes("session")
      ? "“本会话允许”只对这个对话有效，随对话保存，恢复这个对话后仍然有效；“始终允许”写入用户配置的权限规则，可在设置 → 工具与权限里撤销。"
      : "“始终允许”写入用户配置的权限规则，可在设置 → 工具与权限里撤销。"
    : "“本会话允许”只对这个对话有效，随对话保存，恢复这个对话后仍然有效。";
  return <div className="apv-note" id={`remember-where-${target}`}>{t(text)}</div>;
}
