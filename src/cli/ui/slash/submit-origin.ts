export interface SlashSubmitOrigin {
  fromQQ: boolean;
  fromTelegram: boolean;
  fromWeixin: boolean;
}

export function shouldExposeTuiCardsToSlash(origin: SlashSubmitOrigin): boolean {
  return !(origin.fromQQ || origin.fromTelegram || origin.fromWeixin);
}
