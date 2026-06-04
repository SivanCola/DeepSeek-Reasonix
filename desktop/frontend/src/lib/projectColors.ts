export type ProjectColorKey = "" | "red" | "orange" | "amber" | "green" | "teal" | "blue" | "purple" | "pink";

export interface ProjectColorOption {
  key: ProjectColorKey;
  label: string;
  value?: string;
}

export const PROJECT_COLOR_OPTIONS: ProjectColorOption[] = [
  { key: "", label: "默认" },
  { key: "red", label: "红色", value: "#e5534b" },
  { key: "orange", label: "橙色", value: "#d66e4b" },
  { key: "amber", label: "琥珀", value: "#d59a2f" },
  { key: "green", label: "绿色", value: "#4f9f64" },
  { key: "teal", label: "青色", value: "#1f9d93" },
  { key: "blue", label: "蓝色", value: "#3d7be0" },
  { key: "purple", label: "紫色", value: "#8b6de8" },
  { key: "pink", label: "粉色", value: "#cf6ca5" },
];

const colorValues = new Map(PROJECT_COLOR_OPTIONS.map((option) => [option.key, option.value]));

export function projectColorValue(key?: string): string | undefined {
  return colorValues.get((key || "") as ProjectColorKey);
}
