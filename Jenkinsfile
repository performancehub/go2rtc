pipeline {
    agent any

    environment {
        // Github Container Registry Login Details...
        GHCR_USER = credentials('gchr')

        REPO = "ghcr.io"
        CORE_IMAGE = "${REPO}/performancehub/go2rtc"

        TAG_ID = sh(returnStdout: true, script: "git log -1 --oneline --pretty=%h").trim()
        GIT_COMMITTER_NAME = sh(returnStdout: true, script: "git show -s --pretty=%an").trim()
        GIT_MESSAGE = sh(returnStdout: true, script: "git show -s --pretty=%s").trim()

        JENKINS_PROJECT = ""
    }

    stages {
        stage("Set Variables & Configuration") {
            steps {
                script {
                    def alljob = JOB_NAME.tokenize('/') as String[]
                    JENKINS_PROJECT = alljob[0]
                }
            }
        }

        stage("Prepare Build Environment to Support ARM") {
            steps {
                script {
                    // Register QEMU binary formats for cross-platform builds
                    // This enables ARM emulation on amd64 hosts
                    echo "Registering QEMU for multi-architecture builds..."
                    sh "docker run --rm --privileged multiarch/qemu-user-static --reset -p yes"

                    // Detect the parent builder name that supports ARM (linux/arm/v7)
                    def builderName = sh(
                        script: """
                            docker buildx ls | awk '/linux\\/arm\\/v7/ {print prev} {prev=\$1}' | head -n 1
                        """,
                        returnStdout: true
                    ).trim()

                    if (!builderName) {
                        // No suitable builder found, create a new one
                        builderName = "arm_builder_${UUID.randomUUID().toString().substring(0, 8)}"
                        echo "Creating new builder: ${builderName}"
                        sh """
                            docker buildx create --name ${builderName} --use
                            docker buildx inspect ${builderName} --bootstrap
                        """
                    } else {
                        echo "Using builder: ${builderName}"

                        // Check if the builder is active
                        def builderStatus = sh(
                            script: "docker buildx ls | grep ${builderName} | awk 'NR==1{print \$4}'",
                            returnStdout: true
                        ).trim()

                        if (builderStatus != "running") {
                            echo "Builder ${builderName} is inactive. Bootstrapping it..."
                            sh "docker buildx inspect ${builderName} --bootstrap"
                        } else {
                            echo "Builder ${builderName} is already running."
                        }
                    }

                    // Use the detected or newly created builder
                    sh "docker buildx use ${builderName}"
                }
            }
        }

        stage("Build go2rtc Docker Image") {
            parallel {
                stage('go2rtc build') {
                    steps {
                        sh 'docker buildx build --no-cache --platform linux/arm64,linux/amd64,linux/arm/v7 -t "${CORE_IMAGE}:${TAG_ID}" -f Dockerfile .'
                    }
                }
            }
        }

        stage('Deployment') {
            parallel {
                stage('Dev/Stage') {
                    when { branch 'master' }
                    steps {
                        sh 'echo $GHCR_USER_PSW | docker login ghcr.io -u $GHCR_USER_USR --password-stdin'
                        sh 'docker buildx build --push --platform linux/arm64,linux/amd64,linux/arm/v7 -t "${CORE_IMAGE}:staging" -f Dockerfile .'
                    }
                }
                stage('Production') {
                    when { branch 'production' }
                    steps {
                        sh 'echo $GHCR_USER_PSW | docker login ghcr.io -u $GHCR_USER_USR --password-stdin'
                        sh 'docker buildx build --push --platform linux/arm64,linux/amd64,linux/arm/v7 -t "${CORE_IMAGE}:production" -f Dockerfile .'
                        sh 'docker buildx build --push --platform linux/arm64,linux/amd64,linux/arm/v7 -t "${CORE_IMAGE}:latest" -f Dockerfile .'
                    }
                }
            }
        }
    }

    post {
        success {
            slackSend color: "#03CC00", message: "*Build Succeeded (${env.BUILD_NUMBER})*\n Job: ${env.JOB_NAME}\n Commit: ${env.GIT_MESSAGE}\n Author: ${env.GIT_COMMITTER_NAME}\n <${env.RUN_DISPLAY_URL}|Open Jenkins Log>"
        }
        failure {
            slackSend color: "#FF0000", message: "*Build Failed (${env.BUILD_NUMBER})*\n Job: ${env.JOB_NAME}\n Commit: ${env.GIT_MESSAGE}\n Author: ${env.GIT_COMMITTER_NAME}\n <${env.RUN_DISPLAY_URL}|Open Jenkins>"
        }
    }
}