# Coverage matrix

## rotatest/sdk/account

| Symbol | Conditions | Tests |
|---|---:|---|
| CheckProject | 5 | TestCheckProject_DifferentAbsoluteOK, TestCheckProject_EmptyOK, TestCheckProject_RelativeConfigDirRefused, TestCheckProject_RelativeCwdRefused, TestCheckProject_SameDirRefused |
| FindID | 1 | TestFindID_FindsOrNil |
| Label | 1 | TestLabel_EmailThenUUIDThenID |
| MatchIdentity | 5 | TestMatchIdentity_EmailWhenNoUUID, TestMatchIdentity_NeitherMatchesNothing, TestMatchIdentity_NilIdentityMatchesNothing, TestMatchIdentity_OnlySameProvider, TestMatchIdentity_UUIDWinsOverEmail |
| NewAccount | 1 | TestNewAccount_AppliesTokenAndMarksStaged |
| Percent | 4 | TestPercent_IgnoresScoped, TestPercent_MaxOfUnscopedWindows, TestPercent_NeverNegative, TestPercent_NilQuotaZero |
| StagedSuperseded | 1 | TestStagedSuperseded_SetsMarker |
| Status | 6 | TestStatus_DeadIsReauth, TestStatus_ScopedWindowNeverLimits, TestStatus_SpentWindowBeforeResetIsLimited, TestStatus_SpentWindowIsLimited, TestStatus_SpentWindowPastResetIsOK, TestStatus_UnderIsOK |
| String | 1 | TestString_IsProviderSlashLabel |

## rotatest/sdk/claude

| Symbol | Conditions | Tests |
|---|---:|---|
| ClaudeBegin | 1 | TestClaudeBegin_URLIsAuthorizeWithPKCE |
| ClaudeComplete | 10 | TestClaudeComplete_AuthorizationPendingIsSentinel, TestClaudeComplete_CodeWithStateSuffixIsSplit, TestClaudeComplete_ExchangesCodeWithVerifier, TestClaudeComplete_ExpiredAndDeniedAreOAuthErrors, TestClaudeComplete_FallsBackToProfileForIdentity, TestClaudeComplete_InvalidGrantOnCodeIsOAuthError, TestClaudeComplete_MalformedJSONFails, TestClaudeComplete_ProfileWithoutUUIDLeavesIdentityEmpty, TestClaudeComplete_ReplyWithoutAccessTokenFails, TestClaudeComplete_ServerErrorIsHTTPError |
| ClaudeLaunch | 2 | TestClaudeLaunch_ConfigDirComesFromAccountNotHome, TestClaudeLaunch_EnvAndDrops |
| ClaudeRefresh | 6 | TestClaudeRefresh_HonoursContextDeadline, TestClaudeRefresh_InvalidGrantKillsLineage, TestClaudeRefresh_NewRefreshTokenReplacesOld, TestClaudeRefresh_RotatesAccessKeepsRefreshWhenAbsent, TestClaudeRefresh_ScavengesCodeFromObjectShapedError, TestClaudeRefresh_TransientFailureLeavesAccountAlone |
| ClaudeStage | 1 | TestClaudeStage_DeadIsReauth |
| ClaudeStagePlan | 1 | TestClaudeStagePlan_NoFiles |
| ClaudeUsage | 4 | TestClaudeUsage_ErrorStatusIsHTTPError, TestClaudeUsage_OversizeReplyIsRefused, TestClaudeUsage_ParsesWindowsNoteAndExtra, TestClaudeUsage_SendsBetaHeaderAndBearer |

## rotatest/sdk/codex

| Symbol | Conditions | Tests |
|---|---:|---|
| CodexAdopt | 3 | TestCodexAdopt_IgnoresFileOfAnotherAccount, TestCodexAdopt_IgnoresFileThatPredatesLogin, TestCodexAdopt_TakesRotatedRefreshToken |
| CodexAdoptFrom | 1 | TestCodexAdoptFrom_ReadsAnyFS |
| CodexBegin | 1 | TestCodexBegin_URLIsAuthorizeWithPKCE |
| CodexComplete | 3 | TestCodexComplete_AcceptsWholeRedirectURL, TestCodexComplete_FormEncodedExchange, TestCodexComplete_InvalidGrantIsOAuthError |
| CodexDefaults | 1 | TestCodexDefaults_EmptyModelMediumEffort |
| CodexModelsFor | 1 | TestCodexModelsFor_ReadsModelsCache |
| CodexPlan | 2 | TestCodexPlan_NoIDTokenNoRefreshIsReauth, TestCodexPlan_RepairsMissingIDTokenByRefreshing |
| CodexRefresh | 2 | TestCodexRefresh_ReusedTokenIsDead, TestCodexRefresh_SendsScopeAndRotates |
| CodexStage | 1 | TestCodexStage_WritesAuthJSONAndEnv |
| CodexStagePlan | 2 | TestCodexStagePlan_RefusesEmptyHome, TestCodexStagePlan_TouchesNoDiskReturnsAuthJSON |

## rotatest/sdk/grokkimi

| Symbol | Conditions | Tests |
|---|---:|---|
| DelegatedAccount | 1 | TestDelegatedAccount_NeverExpiresRefreshesOrLimits |
| GrokAdopt | 1 | TestGrokAdopt_ReadsDelegatedAuthJSON |
| GrokBegin | 1 | TestGrokBegin_IsAPIKeyAndDelegated |
| GrokComplete | 3 | TestGrokComplete_EmptyKeyIsDelegatedToken, TestGrokComplete_KeyIdentityIsStable, TestGrokComplete_KeyMustStartWithXai |
| GrokLaunch | 4 | TestGrokLaunch_DelegatedUnsignedStillLaunches, TestGrokLaunch_EnvHasKeyAndHome, TestGrokLaunch_NonKeyAccessIsReauth, TestGrokLaunch_RefusesEmptyHome |
| GrokLoginPlan | 1 | TestGrokLoginPlan_DeviceCode |
| GrokResolveModel | 1 | TestGrokResolveModel_FloorPassesUnknownRefusesOtherProviders |
| GrokSignedIn | 1 | TestGrokSignedIn_NeedsAuthJSON |
| Kimi | 1 | TestKimi_HasNoCatalog |
| KimiBegin | 1 | TestKimiBegin_IsDelegated |
| KimiComplete | 1 | TestKimiComplete_RefusesAnythingPasted |
| KimiLaunch | 2 | TestKimiLaunch_EnvHasHome, TestKimiLaunch_RefusesEmptyHomeAndNonDelegated |
| KimiSignedIn | 1 | TestKimiSignedIn_ThreeStates |
| LoginPlanFor | 1 | TestLoginPlanFor_FalseForNonDelegatedAndForClaude |

## rotatest/sdk/login

| Symbol | Conditions | Tests |
|---|---:|---|
| Begin | 9 | TestBegin_BuiltinKinds, TestBegin_CreatedAtFollowsInjectedNow, TestBegin_DelegatedFollowsDelegatorNotKind, TestBegin_EmptyProviderUsesDefaultProvider, TestBegin_IDIsSixHexChars, TestBegin_KindComesFromState, TestBegin_KindDefaultsToCode, TestBegin_ProviderErrorPropagates, TestBegin_UnknownProviderIsInvalidRequest |
| Complete | 8 | TestComplete_DelegatedTokenNeedsNoAccess, TestComplete_IdentifiesWhenTokenHasNoIdentity, TestComplete_IdentifyErrorIsDiscarded, TestComplete_NoAccessTokenIsInvalidRequest, TestComplete_ProviderErrorPropagatesUnchanged, TestComplete_TokenIdentityWinsOverIdentifier, TestComplete_TrimsCode, TestComplete_UnknownProviderInLoginFails |

## rotatest/sdk/quotacatalog

| Symbol | Conditions | Tests |
|---|---:|---|
| Defaults | 1 | TestDefaults_Builtins |
| Efforts | 1 | TestEfforts_Builtins |
| Flavor | 1 | TestFlavor_BuiltinsFlavoredAndUnknownEmpty |
| FlavorsOf | 1 | TestFlavorsOf_UnknownNilKnownCopy |
| Metered | 3 | TestMetered_FakeMeterTrue, TestMetered_OnlyClaudeAmongBuiltins, TestMetered_UnknownFalse |
| Models | 4 | TestModels_BuiltinLists, TestModels_CopiesAliasesToo, TestModels_ReturnsCopies, TestModels_UnknownAndNoCatalogAreNil |
| ModelsFor | 1 | TestModelsFor_AccountCatalogOnlyWithNonEmptyHome |
| NetworkRedirecting | 1 | TestNetworkRedirecting_ListAndCopy |
| PermissionModes | 1 | TestPermissionModes_PerFlavorCopies |
| ResolveEffort | 5 | TestResolveEffort_CaseInsensitive, TestResolveEffort_DefaultWhenEmpty, TestResolveEffort_NoLevelsEmptyOK, TestResolveEffort_NoLevelsWithWantIsError, TestResolveEffort_UnknownListsAccepted |
| ResolveModel | 6 | TestResolveModel_AliasToCanonical, TestResolveModel_EmptyWantIsDefault, TestResolveModel_FloorScansOtherProviders, TestResolveModel_IDCaseInsensitive, TestResolveModel_NoCatalogPassesAnything, TestResolveModel_UnknownListsAccepted |
| Resolved | 1 | TestResolved_ResolvesAgainstHomeAndBlanksEffortForKimi |
| RestrictedFields | 1 | TestRestrictedFields_NameSpecJSONTags |
| Sandboxes | 1 | TestSandboxes_OnlyCodex |
| TakesSandbox | 1 | TestTakesSandbox_CodexAndGrok |
| Usage | 4 | TestUsage_MeterResultVerbatim, TestUsage_NeverCaches, TestUsage_NonMeterIsNilNil, TestUsage_UnknownProviderFails |

## rotatest/sdk/registryjsonerrors

| Symbol | Conditions | Tests |
|---|---:|---|
| Account | 1 | TestAccount_WireShapeIsExact |
| DecodeLenient | 1 | TestDecodeLenient_Reader |
| Encode | 1 | TestEncode_NilSliceIsNull |
| EncodeIndent | 1 | TestEncodeIndent_TwoSpaces |
| EncodeTo | 1 | TestEncodeTo_AppendsNewline |
| HTTPError | 1 | TestHTTPError_MessageTrimsAndTruncates |
| Invalid | 1 | TestInvalid_IsInvalidRequestWithoutPrefix |
| LenientOptions | 1 | TestLenientOptions_DecodesWithJSONv2 |
| Lookup | 2 | TestLookup_EmptyUsesDefaultProviderNotRegistryDefault, TestLookup_UnknownListsKnown |
| NewRegistry | 1 | TestNewRegistry_EmptyNoDefault |
| OAuthError | 1 | TestOAuthError_KeepsCode |
| Providers | 1 | TestProviders_BuiltinsPresent |
| RegistryProviders | 1 | TestRegistryProviders_Sorted |
| RegistryRegister | 1 | TestRegistryRegister_ReplacesByNameOnly |
| Result | 1 | TestResult_WireShapeIsExact |
| Sentinels | 1 | TestSentinels_AreDistinct |
| UnmarshalLenient | 1 | TestUnmarshalLenient_CaseInsensitiveDuplicatesAndBadUTF8 |
| WrapNoAccount | 1 | TestWrapNoAccount_IsNoAccountNamingID |
| WrapNoBinary | 1 | TestWrapNoBinary_IsUnsupportedNotInPath |
| WrapNoLogin | 1 | TestWrapNoLogin_IsNoLoginNamingID |
| WrapReauth | 1 | TestWrapReauth_IsReauthNamingAccount |

## rotatest/sdk/run

| Symbol | Conditions | Tests |
|---|---:|---|
| Run | 24 | TestRun_BufferedArrayOutputIsParsed, TestRun_CodexEventStream, TestRun_ContextCancelKills, TestRun_CwdIsSymlinkResolved, TestRun_DefaultLimitsLeaveSmallOutputAlone, TestRun_GrokBufferedShape, TestRun_HermeticSetsTempConfigDirAndRemovesIt, TestRun_KimiProseBecomesResult, TestRun_MaxBufferedOutputTruncates, TestRun_MaxEventLineTruncates, TestRun_MaxEventsTruncates, TestRun_MaxStderrKeepsTheTail, TestRun_MissingBinaryIsUnsupported, TestRun_NilCommandNeedsBaseEnv, TestRun_NonZeroExitIsAResultNotAnError, TestRun_PromptOnStdinArgvVisibleFieldsFilled, TestRun_ResumeLastBecomesContinue, TestRun_ScratchFilesAreRemoved, TestRun_SignInCheckerConsulted, TestRun_SpacedJSONStillParsed, TestRun_StreamingEventsIncludedOnlyWhenAsked, TestRun_StructuredOutputCaptured, TestRun_SuppliedCommandRunsWithOnlyItsEnv, TestRun_TimeoutKillsAndReturnsResult |

## rotatest/sdk/spec

| Symbol | Conditions | Tests |
|---|---:|---|
| SpecCheck | 28 | TestSpecCheck_BlankPromptRequired, TestSpecCheck_CodexConfigRefused, TestSpecCheck_CwdMustExistAndBeADirectory, TestSpecCheck_DangerousGates, TestSpecCheck_EmptySliceCountsAsSet, TestSpecCheck_EnumRefusals, TestSpecCheck_ExtraNeedsAllowRawFlags, TestSpecCheck_FieldMustBelongToFlavor, TestSpecCheck_FilesCheckedAgainstRoots, TestSpecCheck_MCPFileWithCommandRefused, TestSpecCheck_MCPFileWithURLOnlyOK, TestSpecCheck_MCPInlineRefused, TestSpecCheck_ModelAndEffortResolved, TestSpecCheck_NegativeTimeoutRefusedFirst, TestSpecCheck_NilLimitsSkipsSuppliedConfigChecks, TestSpecCheck_PluginURLsRefused, TestSpecCheck_ReservedFlagsAlwaysRefused, TestSpecCheck_RootsConfineDirectories, TestSpecCheck_RootsRefuseBeforeAnyFileIsRead, TestSpecCheck_RootsResolveSymlinks, TestSpecCheck_SettingSourcesNeedRawFlags, TestSpecCheck_SettingsDenylistDefault, TestSpecCheck_SettingsDenylistReplaced, TestSpecCheck_SettingsFileIsVetted, TestSpecCheck_SettingsFileOver1MBRefused, TestSpecCheck_SettingsFileUnreadableRefused, TestSpecCheck_UnknownFlavorIsUnsupported, TestSpecCheck_WritesNoTempFiles |
| SpecCheckFor | 1 | TestSpecCheckFor_UsesAccountHomeCatalog |
| SpecFor | 1 | TestSpecFor_FillsCwdFromAccountOnlyWhenEmpty |

## rotatest/sdk/staging

| Symbol | Conditions | Tests |
|---|---:|---|
| Adopt | 5 | TestAdopt_AdopterErrorPropagates, TestAdopt_AdopterGetsAccountAndHome, TestAdopt_EmptyHomeIsNilEvenForUnknownProvider, TestAdopt_NonAdopterIsNil, TestAdopt_UnknownProviderFails |
| AdoptFrom | 3 | TestAdoptFrom_FSAdopterGetsFS, TestAdoptFrom_NonFSAdopterIsNil, TestAdoptFrom_UnknownProviderFails |
| Environ | 3 | TestEnviron_NilCommandPanics, TestEnviron_NilInheritedIsJustEnv, TestEnviron_ReplacesDropsAndKeepsOnePerKey |
| LoginPlanFor | 1 | TestLoginPlanFor_NeedsDelegatedAccountAndDelegator |
| OwnsCredentials | 1 | TestOwnsCredentials_AdopterOrDelegator |
| Stage | 5 | TestStage_CreatesHome0700, TestStage_DeadIsReauth, TestStage_EmptyHomeMakesNoDirectory, TestStage_MkdirFailureSurfaces, TestStage_UnknownProviderFails |
| StagePlan | 4 | TestStagePlan_DeadIsReauth, TestStagePlan_NonPlannerLaunchesWithNilFiles, TestStagePlan_PlannerErrorPropagates, TestStagePlan_PlannerTouchesNoDisk |

## rotatest/sdk/token

| Symbol | Conditions | Tests |
|---|---:|---|
| Apply | 4 | TestApply_ClearsDead, TestApply_FoldsIdentityAndMergesExtraAndSetsDelegated, TestApply_KeepsRefreshExpiryScopesWhenAbsent, TestApply_ReplacesWhenPresent |
| Expired | 3 | TestExpired_DelegatedNever, TestExpired_UsesBufferAndNow, TestExpired_ZeroExpiryNever |
| NowMS | 1 | TestNowMS_FollowsInjectedNow |
| Refresh | 8 | TestRefresh_DeadTokenBecomesReauth, TestRefresh_EmptyAccessIsPlainError, TestRefresh_FreshTokenAsksNobody, TestRefresh_NoRefreshTokenIsReauthAndDead, TestRefresh_NonRefresherIsReauthAndDead, TestRefresh_SuccessAppliesAndClearsDead, TestRefresh_TransientErrorLeavesAccount, TestRefresh_UnknownProviderFails |
| Token | 1 | TestToken_RoundTripsThroughEncode |
| When | 2 | TestWhen_NeverFailsToDecode, TestWhen_ParsesRFC3339WithOffsetToUTC |
| Window | 1 | TestWindow_MarshalsMinimalShape |

## rotatest/rotation

| Symbol | Conditions | Tests |
|---|---:|---|
| Available | 1 | TestAvailable_InQueueNotDeadNotSpent |
| Backfill | 3 | TestBackfill_LeavesOrderedStore, TestBackfill_NumbersByIDOnce, TestBackfill_SkipsEmptyStore |
| Choose | 3 | TestChoose_ByIDAndNoAccount, TestChoose_RotationRefreshesMeteredThenPicks, TestChoose_SkipsBusyAccounts |
| Cutoff | 1 | TestCutoff_RangeAndDefault |
| InQueue | 1 | TestInQueue_OrderAtLeastOne |
| Move | 11 | TestMove_BeforeAfterAnother, TestMove_FirstAndLast, TestMove_NumberShiftsLaterAccounts, TestMove_OutClosesGap, TestMove_PastEndIsLast, TestMove_RelativeToSelfOrOutsideIsError, TestMove_RepairsTiesAndGaps, TestMove_SamePlaceShiftsNothing, TestMove_UpAtTopChangesNothing, TestMove_UpDownNeedAPlace, TestMove_UpDownTradeWithNeighbour |
| Next | 1 | TestNext_OnePastHighest |
| ParsePlace | 2 | TestParsePlace_AllForms, TestParsePlace_Refusals |
| Pick | 3 | TestPick_EmptyQueueMessage, TestPick_ExhaustedMessage, TestPick_FirstAvailable |
| Queue | 1 | TestQueue_FiltersAndReturnsNewSlice |
| Sort | 1 | TestSort_OrderedFirstThenOrderThenID |
| Spent | 1 | TestSpent_UsesPercentAgainstCutoff |

## rotatest/store

| Symbol | Conditions | Tests |
|---|---:|---|
| DefaultDir | 1 | TestDefaultDir_HonoursROTAHOME |
| FileBackend | 2 | TestFileBackend_LoadSweepsOldTemp, TestFileBackend_SaveIsAtomic0600 |
| HideFromAgents | 1 | TestHideFromAgents_DedupesAndHostEnvRecomputes |
| NewStore | 5 | TestNewStore_CorruptBytes, TestNewStore_EmptyProviderDefaults, TestNewStore_LoadErrorCloses, TestNewStore_LockErrorReturnsNoStore, TestNewStore_NextIDRaisedPastMaxID |
| Open | 1 | TestOpen_FirstRunIsEmpty |
| Store | 1 | TestStore_IDsAreNeverReused |
| StoreBeginLogin | 3 | TestStoreBeginLogin_ParksPendingJSON0600, TestStoreBeginLogin_SecondStoreOnSameHomeCanFinish, TestStoreBeginLogin_UnknownProviderParksNothing |
| StoreClose | 1 | TestStoreClose_Idempotent |
| StoreFinishLogin | 7 | TestStoreFinishLogin_AuthPendingKeepsPending, TestStoreFinishLogin_ExpiredPendingIsNoLogin, TestStoreFinishLogin_MatchUpdatesInPlace, TestStoreFinishLogin_NoMatchAddsFreshIDAndWipesStaleHome, TestStoreFinishLogin_RejectedCodeKeepsPending, TestStoreFinishLogin_ResetsQuotaAndStaged, TestStoreFinishLogin_UnknownIDIsNoLogin |
| StoreHoldBusy | 3 | TestStoreHoldBusy_ClaimForOwners, TestStoreHoldBusy_MkdirFailureDegradesToOK, TestStoreHoldBusy_NoOpForNonOwners |
| StoreHome | 1 | TestStoreHome_ConfigDirOrRootNoCreate |
| StoreMaintain | 1 | TestStoreMaintain_AdoptsThenRefreshesAndSkipsBusy |
| StorePrepare | 2 | TestStorePrepare_LookPathFailureReleasesClaim, TestStorePrepare_ReturnsBinaryEnvAndClaim |
| StoreRefresh | 5 | TestStoreRefresh_CollectsErrorsNeverFatal, TestStoreRefresh_ForceIgnoresTTL, TestStoreRefresh_PanicInProviderBecomesError, TestStoreRefresh_SavesOnceWhenChanged, TestStoreRefresh_SkipsDeadUnmeteredFreshAndBusy |
| StoreRemove | 3 | TestStoreRemove_BusyIsErrBusy, TestStoreRemove_DeletesHomeAndRetiresID, TestStoreRemove_UnknownIsNoAccount |
| StoreRun | 7 | TestStoreRun_AdoptsBeforeRefresh, TestStoreRun_BusyIsErrBusy, TestStoreRun_ChildEnvIsHostEnvWithoutHiddenNames, TestStoreRun_DeadIsReauth, TestStoreRun_ReleasesLockSoSaveRefuses, TestStoreRun_SavesBeforeRun, TestStoreRun_StageErrorStillSaves |
| StoreSave | 1 | TestStoreSave_AfterReleaseRefuses |

## rotatest/serve

| Symbol | Conditions | Tests |
|---|---:|---|
| AccountSchema | 2 | TestAccountSchema_DescribesOneAccount, TestAccountSchema_UnknownIdIs404 |
| Accounts | 3 | TestAccounts_DefaultSkipsAnAccountOutOfTheQueue, TestAccounts_ListedInRotationOrderWithDefault, TestAccounts_ThresholdReadsTheCutoff |
| Auth | 1 | TestAuth_OldPathsStillWork |
| DeleteAccount | 2 | TestDeleteAccount_BadIdIs400, TestDeleteAccount_RemovesAndThen404 |
| Login | 2 | TestLogin_GrokReturnsIdUrlKind, TestLogin_UnknownProviderIs400 |
| LoginFinish | 3 | TestLoginFinish_ApiKeyAddsAccount, TestLoginFinish_BadIdIs404, TestLoginFinish_WrongKeyKeepsThePendingLogin |
| PatchAccount | 15 | TestPatchAccount_BadOrderIs400, TestPatchAccount_CwdEqualToConfigDirIs400, TestPatchAccount_NothingToChangeIs400, TestPatchAccount_OrderBeforeIdPlacesRelative, TestPatchAccount_OrderFirstShiftsQueue, TestPatchAccount_OrderNumberAsStringIsTheSame, TestPatchAccount_OrderNumberTakesThatPlace, TestPatchAccount_OrderPastTheEndIsLast, TestPatchAccount_OrderUpMovesOnePlace, TestPatchAccount_OrderZeroLeavesTheQueue, TestPatchAccount_ProjectDirsAreStored, TestPatchAccount_RelativeProjectPathIs400, TestPatchAccount_ThresholdIsStored, TestPatchAccount_ThresholdOutOfRangeIs400, TestPatchAccount_UnknownIdIs404 |
| Root | 1 | TestRoot_UnauthenticatedSaysVersion |
| Run | 16 | TestRun_ByIdReturnsTheResultFields, TestRun_ClaudeReadsTheResultEvent, TestRun_CwdInsideRootRuns, TestRun_CwdOutsideRootIs400, TestRun_DangerousOptionIs403WithoutFlag, TestRun_DangerousOptionRunsWithFlag, TestRun_DeadAccountIs409, TestRun_MaxConcurrentSerializesRuns, TestRun_MissingPromptIs400, TestRun_NonZeroExitIs502, TestRun_RotationPicksTheDefault, TestRun_StreamAcceptsNDJSON, TestRun_StreamIsServerSentEvents, TestRun_TimeoutIs504, TestRun_UnknownAccountIs404, TestRun_UnknownFieldIs400 |
| Schema | 1 | TestSchema_DescribesEveryProvider |
| V1 | 3 | TestV1_NeedsBearer, TestV1_TenBadTokensBlockTheAddress, TestV1_WrongTokenIs401 |

## rotatest/live

| Symbol | Conditions | Tests |
|---|---:|---|
| LivePick | 1 | TestLivePick_AgreesWithChoose |
| LiveRefresh | 1 | TestLiveRefresh_ClaudeQuotaHasWindows |
| LiveRun | 1 | TestLiveRun_EachAccountAnswersASmallPrompt |
| LiveSessions | 1 | TestLiveSessions_ScanListsSomething |
| LiveStore | 1 | TestLiveStore_OpensAndListsAccounts |
