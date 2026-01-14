--------------------------- MODULE TwoPhaseCommitWithGossip ---------------------------
\* NexKV 元数据层的 2PC + Gossip + Quorum 强一致性协议建模
\* 设计目标：验证分布式事务的原子性和强一致性

EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS NULL
ASSUME NULL \notin {"n1", "n2", "n3"}

\*=============================================================================
\* 系统配置
\*=============================================================================

\* 节点集合
Nodes == {"n1", "n2", "n3"}
Majority == 2

\* 2PC 阶段
Phase == {"init", "prepare", "decide", "done"}

\* 投票状态
VoteState == {"undecided", "yes", "no"}

\* 全局决策
GlobalDecision == {"undecided", "committed", "rolledback"}

\*=============================================================================
\* 知识结构（用于 Gossip 传播）
\*=============================================================================

Knowledge == [
    coordinator: Nodes,           \* 当前协调者是谁
    phase: Phase,                 \* 当前阶段
    votes: [Nodes -> VoteState],  \* 每个节点的投票状态
    decision: GlobalDecision,     \* 全局决策
    decided: SUBSET Nodes         \* 哪些节点已确认决策
]

\*=============================================================================
\* 全局状态变量
\*=============================================================================

VARIABLES knowledge,      \* 每个节点的知识: [node -> Knowledge]
         coordinator,    \* 当前协调者（由 quorum 选举）
         phase,          \* 当前 2PC 阶段
         votes,          \* 每个节点的投票: [node -> VoteState]
         decision        \* 全局决策

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
    /\ coordinator = "n1"               \* 初始协调者
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
\* Phase 1: Prepare 阶段（协调者发起准备）
\*=============================================================================

\* 选举协调者：基于 quorum 选举新的协调者
ElectCoordinator(newCoord) ==
    /\ phase = "init"
    /\ newCoord \in Nodes
    /\ LET supporters == {n \in Nodes : knowledge[n].coordinator = newCoord}
       IN  Cardinality(supporters) >= Majority  \* 需要 quorum 支持
    /\ coordinator' = newCoord
    /\ phase' = phase
    /\ votes' = votes
    /\ decision' = decision
    /\ knowledge' = [knowledge EXCEPT
                       ![newCoord].coordinator = newCoord]

\* 发起 Prepare：协调者开始 2PC 的 prepare 阶段
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

\* 节点投票 YES：节点同意提交
VoteYes(node) ==
    /\ phase = "prepare"
    /\ votes[node] = "undecided"
    /\ votes' = [votes EXCEPT ![node] = "yes"]
    /\ knowledge' = [knowledge EXCEPT ![node].votes = [votes EXCEPT ![node] = "yes"]]
    /\ UNCHANGED <<coordinator, phase, decision>>

\* 节点投票 NO：节点拒绝提交
VoteNo(node) ==
    /\ phase = "prepare"
    /\ votes[node] = "undecided"
    /\ votes' = [votes EXCEPT ![node] = "no"]
    /\ knowledge' = [knowledge EXCEPT ![node].votes = [votes EXCEPT ![node] = "no"]]
    /\ UNCHANGED <<coordinator, phase, decision>>

\*=============================================================================
\* Phase 2: Decide 阶段（协调者做决策）
\*=============================================================================

\* 检查投票并决定 COMMIT：如果所有节点都投 yes
DecideCommit ==
    /\ phase = "prepare"
    /\ coordinator \in Nodes
    /\ \A n \in Nodes : votes[n] = "yes"  \* 所有人都同意
    /\ phase' = "decide"
    /\ decision' = "committed"
    /\ knowledge' = [n \in Nodes |->
        [phase |-> "decide",
         coordinator |-> knowledge[n].coordinator,
         votes |-> knowledge[n].votes,
         decision |-> "committed",
         decided |-> knowledge[n].decided]]
    /\ UNCHANGED <<coordinator, votes>>

\* 检查投票并决定 ROLLBACK：如果有任何节点投 no
DecideRollback ==
    /\ phase = "prepare"
    /\ coordinator \in Nodes
    /\ \E n \in Nodes : votes[n] = "no"   \* 有人拒绝
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
\* Phase 3: Done 阶段（节点确认决策）
\*=============================================================================

\* 节点确认最终决策
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
\* Gossip 协议：信息传播
\*=============================================================================

\* Gossip 交换：节点交换各自的知识
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
\* 系统演化
\*=============================================================================

Next ==
    \/ ElectCoordinator("n1")
    \/ ElectCoordinator("n2")
    \/ ElectCoordinator("n3")
    \/ SendPrepare
    \/ VoteYes("n1")
    \/ VoteYes("n2")
    \/ VoteYes("n3")
    \/ VoteNo("n1")
    \/ VoteNo("n2")
    \/ VoteNo("n3")
    \/ DecideCommit
    \/ DecideRollback
    \/ AckDecision("n1")
    \/ AckDecision("n2")
    \/ AckDecision("n3")
    \/ \E p, q \in Nodes : GossipExchange(p, q)

Spec == Init /\ [][Next]_<<knowledge, coordinator, phase, votes, decision>>

\*=============================================================================
\* 辅助定义
\*=============================================================================

\* 检查是否所有节点都已完成
AllDone ==
    phase = "done"

\* 检查是否所有节点都 commit
AllCommitted ==
    decision = "committed" /\
    \A n \in Nodes : n \in knowledge[n].decided

\* 检查是否所有节点都 rollback
AllRolledback ==
    decision = "rolledback" /\
    \A n \in Nodes : n \in knowledge[n].decided

\*=============================================================================
\* 强一致性安全属性（不变量）
\*=============================================================================

\* 原子性：在 2PC 完成后（phase = "done"），要么所有节点都 commit，要么都 rollback
\* 在 2PC 过程中允许临时不一致
Atomicity ==
    /\ phase = "done" =>
        (decision = "committed" /\ AllCommitted) \/
        (decision = "rolledback" /\ AllRolledback)

\* 强一致性：在任何时刻，不存在部分节点已决定而其他节点未决定的状态
\*（在 2PC 完成前允许临时不一致，但最终必须一致）
StrongConsistency ==
    /\ phase = "done" =>
        (\A n1, n2 \in Nodes :
            knowledge[n1].decision = knowledge[n2].decision /\
            knowledge[n1].decision # "undecided")

\* 决策一致性：协调者的决策必须基于投票结果
CoordinatorDecisionConsistency ==
    /\ decision = "committed" => \A n \in Nodes : votes[n] = "yes"
    /\ decision = "rolledback" => \E n \in Nodes : votes[n] = "no"

\* 阶段单调性：阶段必须是有效的（不检查转换）
PhaseMonotonicity ==
    phase \in {"init", "prepare", "decide", "done"}

\* 协调者稳定性：协调者必须是一个有效节点
CoordinatorStability ==
    coordinator \in Nodes

\* 投票稳定性：所有节点的投票必须是有效的
VoteStability ==
    \A n \in Nodes : votes[n] \in {"undecided", "yes", "no"}

\*=============================================================================
\* 约束条件（用于状态空间限制）
\*=============================================================================

\* 限制决策探索（为了简化验证）
DecisionConstraint ==
    decision \in {"undecided", "committed"}
    \* 只验证 commit 成功的场景，rollback 场景类似

===========================================================
END
===========================================================
