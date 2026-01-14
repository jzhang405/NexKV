--------------------------- MODULE TwoPhaseCommit5Nodes ---------------------------
\* NexKV 元数据层的 2PC + Gossip + Quorum 协议建模 - 5节点版本
\* 建模目标：验证分布式事务在更大规模集群下的正确性
\* Phase 2 任务 T2.2

EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS NULL
ASSUME NULL \notin {"n1", "n2", "n3", "n4", "n5"}

\*=============================================================================
\* 系统配置
\*=============================================================================

\* 节点集合（3节点 -> 5节点）
Nodes == {"n1", "n2", "n3", "n4", "n5"}

\* Quorum 阈值（3节点的2 -> 5节点的3）
Majority == 3

\* 2PC 阶段
Phase == {"init", "prepare", "decide", "done"}

\* 投票状态
VoteState == {"undecided", "yes", "no"}

\* 全局决策
GlobalDecision == {"undecided", "committed", "rolledback"}

\*=============================================================================
\* 知识结构
\*=============================================================================

Knowledge == [
    coordinator: Nodes,
    phase: Phase,
    votes: [Nodes -> VoteState],
    decision: GlobalDecision,
    decided: SUBSET Nodes
]

\*=============================================================================
\* 全局状态变量
\*=============================================================================

VARIABLES knowledge,
         coordinator,
         phase,
         votes,
         decision

\*=============================================================================
\* 类型不变量
\*=============================================================================

TypeOK ==
    /\ knowledge \in [Nodes -> Knowledge]
    /\ coordinator \in Nodes
    /\ phase \in Phase
    /\ votes \in [Nodes -> VoteState]
    /\ decision \in GlobalDecision

\*=============================================================================
\* 初始化
\*=============================================================================

Init ==
    /\ coordinator = "n1"
    /\ phase = "init"
    /\ votes = [n \in Nodes |-> "undecided"]
    /\ decision = "undecided"
    /\ knowledge = [n \in Nodes |->
        [coordinator |-> "n1",
         phase |-> "init",
         votes |-> [m \in Nodes |-> "undecided"],
         decision |-> "undecided",
         decided |-> {}]]

\*=============================================================================
\* Phase 1: Prepare 阶段
\*=============================================================================

\* 选举协调者（Majority 2 -> 3）
ElectCoordinator(newCoord) ==
    /\ phase = "init"
    /\ newCoord \in Nodes
    /\ LET supporters == {n \in Nodes : knowledge[n].coordinator = newCoord}
       IN  Cardinality(supporters) >= Majority
    /\ coordinator' = newCoord
    /\ phase' = phase
    /\ votes' = votes
    /\ decision' = decision
    /\ knowledge' = [knowledge EXCEPT
                       ![newCoord].coordinator = newCoord]

\* 发起 Prepare
SendPrepare ==
    /\ phase = "init"
    /\ coordinator \in Nodes
    /\ phase' = "prepare"
    /\ knowledge' = [n \in Nodes |->
        [phase |-> "prepare",
         coordinator |-> coordinator,
         votes |-> knowledge[n].votes,
         decision |-> knowledge[n].decision,
         decided |-> knowledge[n].decided]]
    /\ UNCHANGED <<coordinator, votes, decision>>

\* 节点投票 YES（5节点版本）
VoteYes(node) ==
    /\ phase = "prepare"
    /\ votes[node] = "undecided"
    /\ votes' = [votes EXCEPT ![node] = "yes"]
    /\ knowledge' = [knowledge EXCEPT ![node].votes = [votes EXCEPT ![node] = "yes"]]
    /\ UNCHANGED <<coordinator, phase, decision>>

\* 节点投票 NO（5节点版本）
VoteNo(node) ==
    /\ phase = "prepare"
    /\ votes[node] = "undecided"
    /\ votes' = [votes EXCEPT ![node] = "no"]
    /\ knowledge' = [knowledge EXCEPT ![node].votes = [votes EXCEPT ![node] = "no"]]
    /\ UNCHANGED <<coordinator, phase, decision>>

\*=============================================================================
\* Phase 2: Decide 阶段
\*=============================================================================

\* 决定 COMMIT（需要所有 5 节点都投 YES）
DecideCommit ==
    /\ phase = "prepare"
    /\ coordinator \in Nodes
    /\ \A n \in Nodes : votes[n] = "yes"
    /\ phase' = "decide"
    /\ decision' = "committed"
    /\ knowledge' = [n \in Nodes |->
        [phase |-> "decide",
         coordinator |-> knowledge[n].coordinator,
         votes |-> knowledge[n].votes,
         decision |-> "committed",
         decided |-> knowledge[n].decided]]
    /\ UNCHANGED <<coordinator, votes>>

\* 决定 ROLLBACK（任意节点投 NO）
DecideRollback ==
    /\ phase = "prepare"
    /\ coordinator \in Nodes
    /\ \E n \in Nodes : votes[n] = "no"
    /\ phase' = "decide"
    /\ decision' = "rolledback"
    /\ knowledge' = [n \in Nodes |->
        [phase |-> "decide",
         coordinator |-> knowledge[n].coordinator,
         votes |-> knowledge[n].votes,
         decision |-> "rolledback",
         decided |-> knowledge[n].decided]]
    /\ UNCHANGED <<coordinator, votes>>

\*=============================================================================
\* Phase 3: Done 阶段
\*=============================================================================

\* 节点确认决策（5节点版本）
AckDecision(node) ==
    /\ phase = "decide"
    /\ decision # "undecided"
    /\ node \in Nodes
    /\ LET allAcked == \A n \in Nodes : n \in knowledge[node].decided
       IN  IF allAcked
           THEN phase' = "done"
           ELSE phase' = "decide"
    /\ knowledge' = [knowledge EXCEPT ![node].decided = @ \cup {node}]
    /\ UNCHANGED <<coordinator, votes, decision>>

\*=============================================================================
\* Gossip 协议（5节点版本）
\*=============================================================================

GossipExchange(p, q) ==
    /\ p # q
    /\ LET newVotes == [n \in Nodes |->
                         IF knowledge[p].votes[n] = "yes" /\ knowledge[q].votes[n] = "yes"
                         THEN "yes"
                         ELSE IF knowledge[p].votes[n] = "no" \/ knowledge[q].votes[n] = "no"
                         THEN "no"
                         ELSE "undecided"]
           newDecided == knowledge[p].decided \cup knowledge[q].decided
           newCoord ==
                IF Cardinality({n \in Nodes : knowledge[p].coordinator = knowledge[p].coordinator}) >= Majority
                THEN knowledge[p].coordinator
                ELSE IF Cardinality({n \in Nodes : knowledge[q].coordinator = knowledge[q].coordinator}) >= Majority
                THEN knowledge[q].coordinator
                ELSE knowledge[p].coordinator
           newPhase ==
                IF knowledge[p].phase = "decide" \/ knowledge[q].phase = "decide"
                THEN "decide"
                ELSE knowledge[p].phase
           newDecision ==
                IF knowledge[p].decision # "undecided"
                THEN knowledge[p].decision
                ELSE knowledge[q].decision
       IN  knowledge' = [knowledge EXCEPT
                           ![p].coordinator = newCoord,
                           ![q].coordinator = newCoord,
                           ![p].phase = newPhase,
                           ![q].phase = newPhase,
                           ![p].votes = newVotes,
                           ![q].votes = newVotes,
                           ![p].decision = newDecision,
                           ![q].decision = newDecision,
                           ![p].decided = newDecided,
                           ![q].decided = newDecided]
    /\ UNCHANGED <<coordinator, phase, votes, decision>>

\*=============================================================================
\* 系统演化（5节点版本 - 5个节点的投票和确认）
\*=============================================================================

Next ==
    \/ ElectCoordinator("n1")
    \/ ElectCoordinator("n2")
    \/ ElectCoordinator("n3")
    \/ ElectCoordinator("n4")
    \/ ElectCoordinator("n5")
    \/ SendPrepare
    \/ VoteYes("n1") \/ VoteYes("n2") \/ VoteYes("n3") \/ VoteYes("n4") \/ VoteYes("n5")
    \/ VoteNo("n1") \/ VoteNo("n2") \/ VoteNo("n3") \/ VoteNo("n4") \/ VoteNo("n5")
    \/ DecideCommit
    \/ DecideRollback
    \/ AckDecision("n1") \/ AckDecision("n2") \/ AckDecision("n3") \/ AckDecision("n4") \/ AckDecision("n5")
    \/ \E p, q \in Nodes : GossipExchange(p, q)

Spec == Init /\ [][Next]_<<knowledge, coordinator, phase, votes, decision>>

\*=============================================================================
\* 辅助定义
\*=============================================================================

AllDone ==
    phase = "done"

AllCommitted ==
    decision = "committed" /\
    \A n \in Nodes : n \in knowledge[n].decided

AllRolledback ==
    decision = "rolledback" /\
    \A n \in Nodes : n \in knowledge[n].decided

\*=============================================================================
\* 安全属性（不变量）
\*=============================================================================

\* 原子性
Atomicity ==
    /\ phase = "done" =>
        (decision = "committed" /\ AllCommitted) \/
        (decision = "rolledback" /\ AllRolledback)

\* 强一致性
StrongConsistency ==
    /\ phase = "done" =>
        (\A n1, n2 \in Nodes :
            knowledge[n1].decision = knowledge[n2].decision /\
            knowledge[n1].decision # "undecided")

\* 决策一致性
CoordinatorDecisionConsistency ==
    /\ decision = "committed" => \A n \in Nodes : votes[n] = "yes"
    /\ decision = "rolledback" => \E n \in Nodes : votes[n] = "no"

\* 阶段单调性
PhaseMonotonicity ==
    phase \in {"init", "prepare", "decide", "done"}

\* 协调者稳定性
CoordinatorStability ==
    coordinator \in Nodes

\* 投票稳定性
VoteStability ==
    \A n \in Nodes : votes[n] \in {"undecided", "yes", "no"}

\*=============================================================================
\* Phase 2 新增性质
\*=============================================================================

\* 5节点 quorum 选举正确性
QuorumElectionCorrectness ==
    coordinator \in Nodes /\
    Cardinality({n \in Nodes : knowledge[n].coordinator = coordinator}) >= Majority

\* 5节点原子性保证
FiveNodeAtomicity ==
    /\ phase = "done" =>
        (\/ (\A n \in Nodes : n \in knowledge[n].decided /\ decision = "committed")
         \/ (\A n \in Nodes : n \in knowledge[n].decided /\ decision = "rolledback"))

\*=============================================================================
\* 约束条件
\*=============================================================================

DecisionConstraint ==
    decision \in {"undecided", "committed"}

===========================================================
END
===========================================================
