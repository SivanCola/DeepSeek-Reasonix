import { useEffect } from "react";
import type { HubPort, RuntimeView } from "../../port/hub";
import type { AgentPort } from "../../port/port";
import { setAgentOpener } from "../../state/teamOpen";
import { closeGroupStage } from "../../state/teamStage";
import { GroupStage } from "./GroupStage";
import { SeatDrawer } from "./SeatDrawer";

// What the team draws over the panes: a group's chat filling the area, or the
// focused agent's details beside its conversation.
export function TeamLayer({ hub, active, seat, root, port, onMemory, onAdoptPane }: {
  hub: HubPort;
  active: string;
  seat?: RuntimeView["team"];
  root?: string;
  port?: AgentPort | null;
  onMemory?: () => void;
  onAdoptPane: (rt: RuntimeView) => void;
}) {
  useEffect(() => {
    const team = hub.team();
    if (!team) return;
    setAgentOpener(async (agent) => {
      const rt = await team.openAgent(agent);
      closeGroupStage();
      onAdoptPane(rt);
      return rt;
    });
    return () => setAgentOpener(null);
  }, [hub, onAdoptPane]);
  return (
    <>
      <GroupStage hub={hub} active={active} onAdoptPane={onAdoptPane} />
      <SeatDrawer hub={hub} seat={seat} root={root} port={port} onMemory={onMemory} onAdoptPane={onAdoptPane} />
    </>
  );
}
